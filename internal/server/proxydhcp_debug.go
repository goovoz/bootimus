package server

import (
	"encoding/json"
	"fmt"
	"net/http"

	"bootimus/internal/proxydhcp"
)

type proxyDHCPDebugResponse struct {
	Success  bool              `json:"success"`
	Enabled  bool              `json:"enabled"`
	ServerIP string            `json:"server_ip"`
	Bootfile proxyDHCPBootfile `json:"bootfiles"`
	Events   []proxydhcp.Event `json:"events"`
}

type proxyDHCPBootfile struct {
	BIOS  string `json:"bios"`
	UEFI  string `json:"uefi"`
	ARM64 string `json:"arm64"`
}

func (s *Server) proxyDHCPEvents() []proxydhcp.Event {
	if s.proxyDHCPRecorder == nil {
		return []proxydhcp.Event{}
	}
	return s.proxyDHCPRecorder.Events()
}

func (s *Server) handleProxyDHCPEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	bios, uefi, arm64 := s.proxyDHCPBootfiles()
	resp := proxyDHCPDebugResponse{
		Success:  true,
		Enabled:  s.proxyDHCPServer != nil,
		ServerIP: s.config.ServerAddr,
		Bootfile: proxyDHCPBootfile{BIOS: bios, UEFI: uefi, ARM64: arm64},
		Events:   s.proxyDHCPEvents(),
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleProxyDHCPEventsClear(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.proxyDHCPRecorder != nil {
		s.proxyDHCPRecorder.Clear()
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "message": "PXE request history cleared"})
}

func (s *Server) handleProxyDHCPStream(w http.ResponseWriter, r *http.Request) {
	if s.proxyDHCPRecorder == nil {
		http.Error(w, "proxyDHCP recorder unavailable", http.StatusServiceUnavailable)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := s.proxyDHCPRecorder.Subscribe()
	defer s.proxyDHCPRecorder.Unsubscribe(ch)

	fmt.Fprintf(w, "data: {\"type\":\"connected\",\"enabled\":%t}\n\n", s.proxyDHCPServer != nil)
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case ev, open := <-ch:
			if !open {
				return
			}
			body, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "data: %s\n\n", body)
			flusher.Flush()
		}
	}
}
