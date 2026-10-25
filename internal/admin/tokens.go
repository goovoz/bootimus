package admin

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"bootimus/internal/auth"
	"bootimus/internal/models"

	"gorm.io/gorm"
)

const maxAPITokenNameLength = 64

func (h *Handler) APITokens(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.listAPITokens(w)
	case http.MethodPost:
		h.createAPIToken(w, r)
	case http.MethodDelete:
		h.revokeAPIToken(w, r)
	default:
		h.sendJSON(w, http.StatusMethodNotAllowed, Response{Success: false, Error: "Method not allowed"})
	}
}

func (h *Handler) listAPITokens(w http.ResponseWriter) {
	tokens, err := h.storage.ListAPITokens()
	if err != nil {
		h.sendJSON(w, http.StatusInternalServerError, Response{Success: false, Error: err.Error()})
		return
	}
	if tokens == nil {
		tokens = []*models.APIToken{}
	}
	h.sendJSON(w, http.StatusOK, Response{Success: true, Data: tokens})
}

func (h *Handler) createAPIToken(w http.ResponseWriter, r *http.Request) {
	actor := auth.UsernameFromContext(r.Context())
	if auth.IsTokenPrincipal(actor) {
		h.sendJSON(w, http.StatusForbidden, Response{Success: false, Error: "API tokens cannot create other tokens; sign in as a user"})
		return
	}
	var req struct {
		Name          string `json:"name"`
		Scope         string `json:"scope"`
		ExpiresInDays int    `json:"expires_in_days"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.sendJSON(w, http.StatusBadRequest, Response{Success: false, Error: "Invalid request"})
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.Name) > maxAPITokenNameLength {
		h.sendJSON(w, http.StatusBadRequest, Response{Success: false, Error: "A token name of up to 64 characters is required"})
		return
	}
	if req.Scope == "" {
		req.Scope = models.APITokenScopeRead
	}
	if req.Scope != models.APITokenScopeRead && req.Scope != models.APITokenScopeAdmin {
		h.sendJSON(w, http.StatusBadRequest, Response{Success: false, Error: "Scope must be read or admin"})
		return
	}
	if req.ExpiresInDays < 0 {
		h.sendJSON(w, http.StatusBadRequest, Response{Success: false, Error: "Expiry cannot be negative"})
		return
	}
	secret, err := auth.GenerateAPISecret()
	if err != nil {
		h.sendJSON(w, http.StatusInternalServerError, Response{Success: false, Error: "Failed to generate token"})
		return
	}
	token := &models.APIToken{
		Name:      req.Name,
		Prefix:    auth.APISecretPrefix(secret),
		Hash:      auth.HashAPISecret(secret),
		Scope:     req.Scope,
		CreatedBy: actor,
	}
	if req.ExpiresInDays > 0 {
		expires := time.Now().AddDate(0, 0, req.ExpiresInDays)
		token.ExpiresAt = &expires
	}
	if err := h.storage.CreateAPIToken(token); err != nil {
		h.sendJSON(w, http.StatusInternalServerError, Response{Success: false, Error: err.Error()})
		return
	}
	log.Printf("API token created: %s (scope=%s, by=%s)", token.Name, token.Scope, actor)
	h.sendJSON(w, http.StatusCreated, Response{Success: true, Message: "Token created. Copy it now; it will not be shown again.", Data: map[string]interface{}{
		"token":  token,
		"secret": secret,
	}})
}

func (h *Handler) revokeAPIToken(w http.ResponseWriter, r *http.Request) {
	actor := auth.UsernameFromContext(r.Context())
	if auth.IsTokenPrincipal(actor) {
		h.sendJSON(w, http.StatusForbidden, Response{Success: false, Error: "API tokens cannot revoke tokens; sign in as a user"})
		return
	}
	id, err := strconv.ParseUint(r.URL.Query().Get("id"), 10, 64)
	if err != nil || id == 0 {
		h.sendJSON(w, http.StatusBadRequest, Response{Success: false, Error: "Token id is required"})
		return
	}
	token, err := h.storage.GetAPIToken(uint(id))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		h.sendJSON(w, http.StatusNotFound, Response{Success: false, Error: "Token not found"})
		return
	}
	if err != nil {
		h.sendJSON(w, http.StatusInternalServerError, Response{Success: false, Error: err.Error()})
		return
	}
	if err := h.storage.RevokeAPIToken(token.ID); err != nil {
		h.sendJSON(w, http.StatusInternalServerError, Response{Success: false, Error: err.Error()})
		return
	}
	log.Printf("API token revoked: %s (by=%s)", token.Name, actor)
	h.sendJSON(w, http.StatusOK, Response{Success: true, Message: "Token revoked"})
}

func (h *Handler) ResetUserTOTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.sendJSON(w, http.StatusMethodNotAllowed, Response{Success: false, Error: "Method not allowed"})
		return
	}
	user, err := h.lookupUser(r.URL.Query().Get("id"), r.URL.Query().Get("username"))
	if err != nil {
		h.sendJSON(w, http.StatusNotFound, Response{Success: false, Error: "User not found"})
		return
	}
	if !user.TOTPEnabled && user.TOTPSecret == "" {
		h.sendJSON(w, http.StatusBadRequest, Response{Success: false, Error: "Two-factor authentication is not enabled for " + user.Username})
		return
	}
	auth.ClearTOTP(user)
	if err := h.storage.UpdateUser(user.Username, user); err != nil {
		h.sendJSON(w, http.StatusInternalServerError, Response{Success: false, Error: err.Error()})
		return
	}
	log.Printf("Two-factor authentication reset for user %s by %s", user.Username, auth.UsernameFromContext(r.Context()))
	h.sendJSON(w, http.StatusOK, Response{Success: true, Message: "Two-factor authentication removed for " + user.Username})
}

func (h *Handler) lookupUser(idParam, username string) (*models.User, error) {
	if username != "" {
		return h.storage.GetUser(username)
	}
	id, err := strconv.ParseUint(idParam, 10, 64)
	if err != nil || id == 0 {
		return nil, errors.New("user id or username is required")
	}
	users, err := h.storage.ListUsers()
	if err != nil {
		return nil, err
	}
	for _, u := range users {
		if uint64(u.ID) == id {
			return u, nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}
