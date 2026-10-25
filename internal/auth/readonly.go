package auth

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"strings"
)

var viewerRedactedFields = map[string]bool{
	"ipmi_password":         true,
	"config_patch":          true,
	"chap_password":         true,
	"reverse_chap_password": true,
}

func (m *Manager) ReadMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims, ok := m.authenticate(w, r)
		if !ok {
			return
		}
		r = withUsername(r, claims.Username)
		if claims.IsAdmin {
			next(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Your account has read-only access"})
			return
		}
		rw := &redactingWriter{ResponseWriter: w}
		next(rw, r)
		rw.finish()
	}
}

func (m *Manager) HandleMe(w http.ResponseWriter, r *http.Request) {
	claims, ok := m.authenticate(w, r)
	if !ok {
		return
	}
	role := "read-only"
	if claims.IsAdmin {
		role = "admin"
	}
	principal := "user"
	source := ""
	totpEnabled := false
	if IsTokenPrincipal(claims.Username) {
		principal = "token"
	} else if user, err := m.userStore.GetUser(claims.Username); err == nil {
		source = user.AuthSource()
		totpEnabled = user.TOTPEnabled
	}
	data := map[string]interface{}{
		"username":     claims.Username,
		"is_admin":     claims.IsAdmin,
		"role":         role,
		"principal":    principal,
		"source":       source,
		"totp_enabled": totpEnabled,
	}
	if m.MeExtras != nil {
		for k, v := range m.MeExtras() {
			data[k] = v
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "data": data})
}

type redactingWriter struct {
	http.ResponseWriter
	decided bool
	isJSON  bool
	status  int
	buf     bytes.Buffer
}

func (rw *redactingWriter) decide() {
	if rw.decided {
		return
	}
	rw.decided = true
	rw.isJSON = strings.Contains(strings.ToLower(rw.Header().Get("Content-Type")), "json")
}

func (rw *redactingWriter) WriteHeader(status int) {
	rw.decide()
	if rw.isJSON {
		rw.status = status
		return
	}
	rw.ResponseWriter.WriteHeader(status)
}

func (rw *redactingWriter) Write(p []byte) (int, error) {
	rw.decide()
	if rw.isJSON {
		return rw.buf.Write(p)
	}
	return rw.ResponseWriter.Write(p)
}

func (rw *redactingWriter) Flush() {
	if rw.isJSON {
		return
	}
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (rw *redactingWriter) finish() {
	if !rw.isJSON {
		return
	}
	rw.Header().Del("Content-Length")

	body, err := redactJSON(rw.buf.Bytes())
	if err != nil {
		log.Printf("Auth: Refusing to send unparseable JSON to a read-only user: %v", err)
		rw.ResponseWriter.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(rw.ResponseWriter).Encode(map[string]interface{}{"success": false, "error": "Failed to prepare response"})
		return
	}

	if rw.status != 0 {
		rw.ResponseWriter.WriteHeader(rw.status)
	}
	rw.ResponseWriter.Write(body)
}

func redactJSON(body []byte) ([]byte, error) {
	if len(bytes.TrimSpace(body)) == 0 {
		return body, nil
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var v interface{}
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	redactValue(v)
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func redactValue(v interface{}) {
	switch t := v.(type) {
	case map[string]interface{}:
		for k, child := range t {
			if viewerRedactedFields[k] {
				delete(t, k)
				continue
			}
			redactValue(child)
		}
	case []interface{}:
		for _, child := range t {
			redactValue(child)
		}
	}
}
