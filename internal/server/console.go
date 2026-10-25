package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"bootimus/internal/bmc"
	"bootimus/internal/models"

	"github.com/gorilla/websocket"
)

const (
	consoleIdleTimeout    = 30 * time.Minute
	consoleConnectTimeout = 20 * time.Second
	consoleSubprotocol    = "bearer"
)

type consoleSessions struct {
	mu     sync.Mutex
	active map[string]bool
}

func (c *consoleSessions) acquire(mac string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.active == nil {
		c.active = map[string]bool{}
	}
	if c.active[mac] {
		return false
	}
	c.active[mac] = true
	return true
}

func (c *consoleSessions) release(mac string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.active, mac)
}

var consoleUpgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	Subprotocols:    []string{consoleSubprotocol},
}

func consoleToken(r *http.Request) string {
	if t := r.URL.Query().Get("token"); t != "" {
		return t
	}
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	var offered []string
	for _, h := range r.Header.Values("Sec-WebSocket-Protocol") {
		for _, p := range strings.Split(h, ",") {
			if p = strings.TrimSpace(p); p != "" {
				offered = append(offered, p)
			}
		}
	}
	for i, p := range offered {
		if p == consoleSubprotocol && i+1 < len(offered) {
			return offered[i+1]
		}
	}
	return ""
}

func (s *Server) resolveBMCTarget(c *models.Client) (bmc.Target, bool) {
	var group *models.ClientGroup
	if c.ClientGroupID != nil {
		if g, err := s.config.Storage.GetClientGroup(*c.ClientGroupID); err == nil {
			group = g
		}
	}
	return bmc.Resolve(c, group)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": msg})
}

type consoleWriter struct {
	mu   sync.Mutex
	conn *websocket.Conn
}

func (w *consoleWriter) binary(data []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.conn.WriteMessage(websocket.BinaryMessage, data)
}

func (w *consoleWriter) status(kind, message string) error {
	body, _ := json.Marshal(map[string]string{"type": kind, "message": message})
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.conn.WriteMessage(websocket.TextMessage, body)
}

func (w *consoleWriter) close(code int, reason string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, reason), time.Now().Add(time.Second))
}

func (s *Server) handleClientConsole(w http.ResponseWriter, r *http.Request) {
	username := "anonymous"
	if s.config.Auth != nil {
		token := consoleToken(r)
		if token == "" {
			writeJSONError(w, http.StatusUnauthorized, "Authentication required")
			return
		}
		claims, err := s.config.Auth.AuthenticateToken(token)
		if err != nil {
			writeJSONError(w, http.StatusUnauthorized, err.Error())
			return
		}
		if !claims.IsAdmin {
			writeJSONError(w, http.StatusForbidden, "Administrator privileges required")
			return
		}
		username = claims.Username
	}
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	mac := strings.ToLower(strings.ReplaceAll(r.URL.Query().Get("mac"), "-", ":"))
	if mac == "" {
		writeJSONError(w, http.StatusBadRequest, "mac required")
		return
	}
	if s.config.Storage == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "Console requires a database")
		return
	}
	client, err := s.config.Storage.GetClient(mac)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "Client not found")
		return
	}
	target, ok := s.resolveBMCTarget(client)
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "Client has no BMC host and credentials (set on the client or its group)")
		return
	}
	if !s.consoles.acquire(mac) {
		writeJSONError(w, http.StatusConflict, "A console session is already open for this client")
		return
	}
	defer s.consoles.release(mac)

	conn, err := consoleUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	ws := &consoleWriter{conn: conn}

	open := s.openConsole
	if open == nil {
		open = bmc.OpenConsole
	}
	ctx, cancel := context.WithTimeout(r.Context(), consoleConnectTimeout)
	console, err := open(ctx, target)
	cancel()
	if err != nil {
		log.Printf("Console: %s could not open console for %s (%s via %s): %v", username, mac, target.Host, target.Protocol, err)
		ws.status("error", err.Error())
		ws.close(websocket.CloseInternalServerErr, "console unavailable")
		return
	}
	log.Printf("Console: %s opened console for %s (%s via %s)", username, mac, target.Host, target.Protocol)
	ws.status("connected", fmt.Sprintf("Connected to %s via %s", target.Host, consoleTransport(target.Protocol)))

	var lastActivity atomic.Int64
	touch := func() { lastActivity.Store(time.Now().UnixNano()) }
	touch()

	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 4096)
		for {
			n, err := console.Read(buf)
			if n > 0 {
				touch()
				if werr := ws.binary(buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				if !errors.Is(err, io.EOF) {
					ws.status("closed", err.Error())
				} else {
					ws.status("closed", "Console closed by the remote end")
				}
				ws.close(websocket.CloseNormalClosure, "console closed")
				return
			}
		}
	}()

	idle := time.NewTicker(time.Minute)
	defer idle.Stop()
	stopIdle := make(chan struct{})
	go func() {
		for {
			select {
			case <-stopIdle:
				return
			case <-done:
				return
			case <-idle.C:
				if time.Since(time.Unix(0, lastActivity.Load())) > consoleIdleTimeout {
					ws.status("closed", "Console closed after 30 minutes without activity")
					console.Close()
					return
				}
			}
		}
	}()

	for {
		mt, data, err := conn.ReadMessage()
		if err != nil {
			break
		}
		touch()
		if mt == websocket.TextMessage && isConsoleControl(data) {
			continue
		}
		if _, err := console.Write(data); err != nil {
			ws.status("closed", err.Error())
			break
		}
	}
	close(stopIdle)
	console.Close()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
	}
	log.Printf("Console: %s closed console for %s", username, mac)
}

func isConsoleControl(data []byte) bool {
	if len(data) == 0 || data[0] != '{' {
		return false
	}
	var msg struct {
		Type string `json:"type"`
	}
	return json.Unmarshal(data, &msg) == nil && msg.Type != ""
}

func consoleTransport(protocol string) string {
	switch protocol {
	case "amt":
		return "Intel AMT Serial-over-LAN"
	case "redfish", "":
		return "IPMI Serial-over-LAN"
	default:
		return protocol
	}
}
