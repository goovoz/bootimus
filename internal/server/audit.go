package server

import (
	"encoding/json"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"bootimus/internal/auth"
	"bootimus/internal/models"
	"bootimus/internal/storage"
	"bootimus/internal/webhook"
)

type auditRecorder struct {
	store    storage.Storage
	notifier *webhook.Notifier
	wg       sync.WaitGroup
}

type auditStatusWriter struct {
	http.ResponseWriter
	status int
}

func (w *auditStatusWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *auditStatusWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(p)
}

func (w *auditStatusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

var auditedMethods = map[string]bool{
	http.MethodPost:   true,
	http.MethodPut:    true,
	http.MethodPatch:  true,
	http.MethodDelete: true,
}

func (a *auditRecorder) wrap(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if a == nil || a.store == nil || !auditedMethods[r.Method] || r.URL.Path == "/api/login" {
			next(w, r)
			return
		}
		sw := &auditStatusWriter{ResponseWriter: w}
		next(sw, r)
		status := sw.status
		if status == 0 {
			status = http.StatusOK
		}
		username := auth.UsernameFromContext(r.Context())
		if username == "" {
			username = "anonymous"
		}
		entry := &models.AuditLog{
			Username: username,
			Method:   r.Method,
			Path:     r.URL.Path,
			Query:    redactQuery(r.URL.Query()),
			Status:   status,
			RemoteIP: remoteIP(r.RemoteAddr),
			Summary:  auditSummary(r.Method, r.URL.Path, r.URL.Query()),
		}
		a.wg.Add(1)
		go a.record(entry)
	}
}

func (a *auditRecorder) record(entry *models.AuditLog) {
	defer a.wg.Done()
	if err := a.store.CreateAuditLog(entry); err != nil {
		log.Printf("Audit: failed to record %s %s by %s: %v", entry.Method, entry.Path, entry.Username, err)
		return
	}
	a.notifier.Fire(webhook.Event{
		Event:     webhook.EventAdminAction,
		Timestamp: entry.CreatedAt,
		IP:        entry.RemoteIP,
		Metadata: map[string]string{
			"username": entry.Username,
			"method":   entry.Method,
			"path":     entry.Path,
			"query":    entry.Query,
			"status":   strconv.Itoa(entry.Status),
			"summary":  entry.Summary,
		},
	})
}

func (a *auditRecorder) wait() {
	if a != nil {
		a.wg.Wait()
	}
}

var redactedQueryKeys = []string{"password", "token", "secret"}

func redactQuery(values url.Values) string {
	if len(values) == 0 {
		return ""
	}
	redacted := url.Values{}
	for key, vals := range values {
		lower := strings.ToLower(key)
		hide := false
		for _, needle := range redactedQueryKeys {
			if strings.Contains(lower, needle) {
				hide = true
				break
			}
		}
		for _, v := range vals {
			if hide {
				v = "[redacted]"
			}
			redacted.Add(key, v)
		}
	}
	return redacted.Encode()
}

func remoteIP(remoteAddr string) string {
	if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
		return host
	}
	return remoteAddr
}

var auditIdentifierKeys = []string{"mac", "filename", "name", "username", "id", "distro", "set"}

func auditSummary(method, path string, values url.Values) string {
	segments := strings.Split(strings.Trim(strings.TrimPrefix(path, "/api"), "/"), "/")
	if len(segments) == 0 || segments[0] == "" {
		return method + " " + path
	}
	resource := singular(strings.ReplaceAll(segments[0], "-", " "))
	var verb string
	if len(segments) > 1 {
		verb = humanise(strings.Join(segments[1:], " "))
	} else {
		switch method {
		case http.MethodPost:
			verb = "Created"
		case http.MethodDelete:
			verb = "Deleted"
		default:
			verb = "Updated"
		}
	}
	summary := verb + " " + resource
	for _, key := range auditIdentifierKeys {
		if v := values.Get(key); v != "" {
			return summary + " " + v
		}
	}
	return summary
}

func humanise(s string) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "-", " "), "_", " ")
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func singular(s string) string {
	switch {
	case strings.HasSuffix(s, "ies"):
		return strings.TrimSuffix(s, "ies") + "y"
	case strings.HasSuffix(s, "sses"):
		return strings.TrimSuffix(s, "es")
	case strings.HasSuffix(s, "s") && !strings.HasSuffix(s, "ss"):
		return strings.TrimSuffix(s, "s")
	}
	return s
}

func (s *Server) handleAuditList(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Method not allowed"})
		return
	}
	if s.config.Storage == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Audit log requires a database"})
		return
	}
	q := r.URL.Query()
	filter := storage.AuditLogFilter{
		Username: strings.TrimSpace(q.Get("username")),
		Path:     strings.TrimSpace(q.Get("path")),
		Limit:    100,
	}
	if raw := q.Get("since"); raw != "" {
		since, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "since must be an RFC 3339 timestamp"})
			return
		}
		filter.Since = since
	}
	if raw := q.Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			filter.Limit = n
		}
	}
	if filter.Limit > 1000 {
		filter.Limit = 1000
	}
	if raw := q.Get("offset"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			filter.Offset = n
		}
	}
	entries, total, err := s.config.Storage.ListAuditLogs(filter)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": err.Error()})
		return
	}
	if entries == nil {
		entries = []models.AuditLog{}
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"data": map[string]interface{}{
			"entries": entries,
			"total":   total,
		},
	})
}
