package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"bootimus/internal/models"
	"bootimus/internal/webhook"
)

const (
	defaultInstallTimeout = 6 * time.Hour
	installSweepInterval  = 5 * time.Minute
	installMessageLimit   = 500
)

func newInstallToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

func (s *Server) installTimeout() time.Duration {
	if s.config.InstallTimeoutHours > 0 {
		return time.Duration(s.config.InstallTimeoutHours) * time.Hour
	}
	return defaultInstallTimeout
}

func (s *Server) installCallbackURL(token string) string {
	return fmt.Sprintf("http://%s:%d/install/callback?token=%s", s.config.ServerAddr, s.config.HTTPPort, token)
}

func (s *Server) noteImageBoot(mac string, image *models.Image, client *models.Client) {
	if s.config.Storage == nil || image == nil || image.BootMethod != "kernel" {
		return
	}
	if s.resolveAutoInstallType(image, client) == "" {
		return
	}
	if rec, err := s.config.Storage.LatestOpenInstallRecord(mac, image.Filename); err == nil && time.Since(rec.BootedAt) < s.installTimeout() {
		return
	}
	now := time.Now()
	rec := &models.InstallRecord{
		MACAddress:    mac,
		ImageFilename: image.Filename,
		ImageName:     image.Name,
		Token:         newInstallToken(),
		State:         models.InstallStateBooted,
		BootedAt:      now,
	}
	if err := s.config.Storage.CreateInstallRecord(rec); err != nil {
		log.Printf("Install tracking: failed to record boot for %s: %v", mac, err)
		return
	}
	if err := s.config.Storage.SetClientInstallStatus(mac, models.InstallStateBooted, image.Name, now); err != nil {
		log.Printf("Install tracking: failed to update client %s: %v", mac, err)
	}
}

func (s *Server) noteAutoInstallFetch(mac string, image *models.Image) string {
	if s.config.Storage == nil || mac == "" || image == nil {
		return ""
	}
	now := time.Now()
	rec, err := s.config.Storage.LatestOpenInstallRecord(mac, image.Filename)
	if err != nil {
		rec = &models.InstallRecord{
			MACAddress:    mac,
			ImageFilename: image.Filename,
			ImageName:     image.Name,
			Token:         newInstallToken(),
			State:         models.InstallStateInstalling,
			BootedAt:      now,
			InstallingAt:  &now,
		}
		if err := s.config.Storage.CreateInstallRecord(rec); err != nil {
			log.Printf("Install tracking: failed to record install start for %s: %v", mac, err)
			return ""
		}
	} else if rec.State == models.InstallStateBooted {
		rec.State = models.InstallStateInstalling
		rec.InstallingAt = &now
		if err := s.config.Storage.UpdateInstallRecord(rec); err != nil {
			log.Printf("Install tracking: failed to mark %s as installing: %v", mac, err)
		}
	} else {
		return rec.Token
	}
	if err := s.config.Storage.SetClientInstallStatus(mac, models.InstallStateInstalling, image.Name, now); err != nil {
		log.Printf("Install tracking: failed to update client %s: %v", mac, err)
	}
	s.logAndBroadcast("Client %s: unattended install of %s started", mac, image.Name)
	return rec.Token
}

func installStateFromReport(status string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "completed", "complete", "success", "ok", "done":
		return models.InstallStateCompleted, true
	case "failed", "failure", "error", "fail":
		return models.InstallStateFailed, true
	}
	return "", false
}

func (s *Server) handleInstallCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.config.Storage == nil {
		http.Error(w, "Install reporting requires a database", http.StatusServiceUnavailable)
		return
	}
	r.ParseForm()
	token := strings.TrimSpace(r.FormValue("token"))
	if token == "" {
		http.Error(w, "Missing token", http.StatusBadRequest)
		return
	}
	state, ok := installStateFromReport(r.FormValue("status"))
	if !ok {
		http.Error(w, "status must be completed or failed", http.StatusBadRequest)
		return
	}
	message := strings.TrimSpace(r.FormValue("message"))
	if len(message) > installMessageLimit {
		message = message[:installMessageLimit]
	}
	rec, err := s.config.Storage.GetInstallRecordByToken(token)
	if err != nil {
		http.Error(w, "Unknown install token", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if !rec.Open() {
		fmt.Fprintf(w, "already recorded as %s\n", rec.State)
		return
	}
	ip := r.RemoteAddr
	if i := strings.LastIndex(ip, ":"); i > 0 {
		ip = ip[:i]
	}
	s.finishInstall(rec, state, message, ip)
	fmt.Fprintf(w, "ok\n")
}

func (s *Server) finishInstall(rec *models.InstallRecord, state, message, ip string) {
	now := time.Now()
	rec.State = state
	rec.FinishedAt = &now
	rec.Message = message
	rec.ReporterIP = ip
	if err := s.config.Storage.UpdateInstallRecord(rec); err != nil {
		log.Printf("Install tracking: failed to finish record %d: %v", rec.ID, err)
		return
	}
	if err := s.config.Storage.SetClientInstallStatus(rec.MACAddress, state, rec.ImageName, now); err != nil {
		log.Printf("Install tracking: failed to update client %s: %v", rec.MACAddress, err)
	}
	clientName := ""
	if c, err := s.config.Storage.GetClient(rec.MACAddress); err == nil {
		clientName = c.Name
	}
	event := webhook.EventInstallDone
	if state == models.InstallStateFailed {
		event = webhook.EventInstallFailed
	}
	metadata := map[string]string{
		"image_filename":   rec.ImageFilename,
		"duration_seconds": strconv.Itoa(int(now.Sub(rec.BootedAt).Seconds())),
	}
	if message != "" {
		metadata["message"] = message
	}
	s.webhookNotifier.Fire(webhook.Event{
		Event:      event,
		MAC:        rec.MACAddress,
		ClientName: clientName,
		Image:      rec.ImageName,
		IP:         ip,
		Metadata:   metadata,
	})
	if message != "" {
		s.logAndBroadcast("Client %s: install of %s %s (%s)", rec.MACAddress, rec.ImageName, state, message)
	} else {
		s.logAndBroadcast("Client %s: install of %s %s", rec.MACAddress, rec.ImageName, state)
	}
}

func (s *Server) sweepStaleInstalls() int {
	if s.config.Storage == nil {
		return 0
	}
	timeout := s.installTimeout()
	stale, err := s.config.Storage.ListOpenInstallRecordsBefore(time.Now().Add(-timeout))
	if err != nil {
		log.Printf("Install tracking: failed to list stale installs: %v", err)
		return 0
	}
	for i := range stale {
		s.finishInstall(&stale[i], models.InstallStateFailed, fmt.Sprintf("timed out after %s without a completion report", timeout), "")
	}
	return len(stale)
}

func (s *Server) startInstallSweeper() {
	if s.config.Storage == nil {
		return
	}
	s.installSweepOnce.Do(func() {
		s.installSweepStop = make(chan struct{})
		s.installSweepWG.Add(1)
		go func() {
			defer s.installSweepWG.Done()
			ticker := time.NewTicker(installSweepInterval)
			defer ticker.Stop()
			for {
				select {
				case <-s.installSweepStop:
					return
				case <-ticker.C:
					s.sweepStaleInstalls()
				}
			}
		}()
	})
}

func (s *Server) stopInstallSweeper() {
	if s.installSweepStop == nil {
		return
	}
	select {
	case <-s.installSweepStop:
	default:
		close(s.installSweepStop)
	}
	s.installSweepWG.Wait()
}

func (s *Server) writeInstallJSON(w http.ResponseWriter, status int, payload map[string]interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(payload)
}

func installLimit(r *http.Request, def, max int) int {
	n, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || n <= 0 {
		return def
	}
	if n > max {
		return max
	}
	return n
}

func (s *Server) handleClientInstalls(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeInstallJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{"success": false, "error": "Method not allowed"})
		return
	}
	if s.config.Storage == nil {
		s.writeInstallJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"success": false, "error": "Install history requires a database"})
		return
	}
	mac := normaliseMenuMAC(r.URL.Query().Get("mac"))
	if mac == "unknown" {
		s.writeInstallJSON(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": "mac is required"})
		return
	}
	recs, err := s.config.Storage.ListInstallRecords(mac, installLimit(r, 50, 500))
	if err != nil {
		s.writeInstallJSON(w, http.StatusInternalServerError, map[string]interface{}{"success": false, "error": err.Error()})
		return
	}
	if recs == nil {
		recs = []models.InstallRecord{}
	}
	s.writeInstallJSON(w, http.StatusOK, map[string]interface{}{"success": true, "data": recs})
}

type installListEntry struct {
	models.InstallRecord
	ClientName string `json:"client_name,omitempty"`
}

func (s *Server) handleRecentInstalls(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeInstallJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{"success": false, "error": "Method not allowed"})
		return
	}
	if s.config.Storage == nil {
		s.writeInstallJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"success": false, "error": "Install history requires a database"})
		return
	}
	recs, err := s.config.Storage.ListInstallRecords("", installLimit(r, 100, 1000))
	if err != nil {
		s.writeInstallJSON(w, http.StatusInternalServerError, map[string]interface{}{"success": false, "error": err.Error()})
		return
	}
	names := map[string]string{}
	if clients, err := s.config.Storage.ListClients(); err == nil {
		for _, c := range clients {
			names[c.MACAddress] = c.Name
		}
	}
	entries := make([]installListEntry, 0, len(recs))
	for _, rec := range recs {
		entries = append(entries, installListEntry{InstallRecord: rec, ClientName: names[rec.MACAddress]})
	}
	s.writeInstallJSON(w, http.StatusOK, map[string]interface{}{"success": true, "data": entries})
}
