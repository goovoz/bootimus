package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"bootimus/internal/models"
)

const (
	APITokenPrefix       = "btk_"
	apiTokenSecretLength = 40
	apiTokenTouchEvery   = time.Minute
	TokenUsernamePrefix  = "token:"
)

const base62 = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

type TokenStore interface {
	GetAPITokenByHash(hash string) (*models.APIToken, error)
	TouchAPIToken(id uint, at time.Time) error
}

var ErrTokenRejected = errors.New("API token is invalid, revoked or expired")

func GenerateAPISecret() (string, error) {
	var sb strings.Builder
	sb.WriteString(APITokenPrefix)
	max := big.NewInt(int64(len(base62)))
	for i := 0; i < apiTokenSecretLength; i++ {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		sb.WriteByte(base62[n.Int64()])
	}
	return sb.String(), nil
}

func HashAPISecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

func APISecretPrefix(secret string) string {
	if len(secret) < 8 {
		return secret
	}
	return secret[:8]
}

func IsAPISecret(bearer string) bool {
	return strings.HasPrefix(bearer, APITokenPrefix)
}

func IsTokenPrincipal(username string) bool {
	return strings.HasPrefix(username, TokenUsernamePrefix)
}

type tokenTouches struct {
	mu   sync.Mutex
	last map[uint]time.Time
}

func (m *Manager) tokenStore() TokenStore {
	ts, _ := m.userStore.(TokenStore)
	return ts
}

func (m *Manager) authenticateAPIToken(secret string) (*Claims, error) {
	store := m.tokenStore()
	if store == nil {
		return nil, ErrTokenRejected
	}
	token, err := store.GetAPITokenByHash(HashAPISecret(secret))
	if err != nil {
		return nil, ErrTokenRejected
	}
	now := time.Now()
	if !token.Usable(now) {
		return nil, ErrTokenRejected
	}
	m.touchToken(store, token.ID, now)
	return &Claims{
		Username: TokenUsernamePrefix + token.Name,
		IsAdmin:  token.Scope == models.APITokenScopeAdmin,
	}, nil
}

func (m *Manager) touchToken(store TokenStore, id uint, now time.Time) {
	m.touches.mu.Lock()
	last, seen := m.touches.last[id]
	if seen && now.Sub(last) < apiTokenTouchEvery {
		m.touches.mu.Unlock()
		return
	}
	if m.touches.last == nil {
		m.touches.last = map[uint]time.Time{}
	}
	m.touches.last[id] = now
	m.touches.mu.Unlock()
	_ = store.TouchAPIToken(id, now)
}

func bearerToken(r *http.Request) (string, bool) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
		return "", false
	}
	return strings.TrimPrefix(authHeader, "Bearer "), true
}
