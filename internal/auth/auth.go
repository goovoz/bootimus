package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"bootimus/internal/database"
	"bootimus/internal/models"

	"github.com/golang-jwt/jwt/v5"
)

type Manager struct {
	userStore   database.UserStore
	jwtSecret   []byte
	ldapConfig  *LDAPConfig
	oidc        *oidcClient
	loginLimit  *loginLimiter
	limiterOnce sync.Once
	MeExtras    func() map[string]interface{}
	touches     tokenTouches
}

func (m *Manager) limiter() *loginLimiter {
	m.limiterOnce.Do(func() {
		if m.loginLimit == nil {
			m.loginLimit = newLoginLimiter()
		}
	})
	return m.loginLimit
}

type Claims struct {
	Username string `json:"username"`
	IsAdmin  bool   `json:"is_admin"`
	jwt.RegisteredClaims
}

type LoginRequest struct {
	Username   string `json:"username"`
	Password   string `json:"password"`
	AuthMethod string `json:"auth_method"`
	Code       string `json:"code"`
}

type LoginResponse struct {
	Token    string `json:"token"`
	Username string `json:"username"`
	IsAdmin  bool   `json:"is_admin"`
}

func NewManager(userStore database.UserStore, ldapConfig *LDAPConfig, oidcConfig *OIDCConfig) (*Manager, error) {
	if userStore == nil {
		return nil, fmt.Errorf("userStore is required for authentication")
	}

	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("failed to generate JWT secret: %w", err)
	}

	m := &Manager{
		userStore: userStore,
		jwtSecret: secret,
	}

	if ldapConfig != nil && ldapConfig.IsConfigured() {
		m.ldapConfig = ldapConfig
		log.Printf("LDAP authentication enabled (server: %s)", m.ldapConfig.serverURL())
	}

	if oidcConfig != nil && oidcConfig.IsConfigured() {
		m.oidc = newOIDCClient(oidcConfig)
		log.Printf("OIDC authentication enabled (issuer: %s)", oidcConfig.Issuer)
		if len(oidcConfig.AdminGroups) == 0 && len(oidcConfig.AdminUsers) == 0 {
			log.Println("OIDC: No admin groups or users configured; OIDC users will not be given admin rights")
		}
		if len(oidcConfig.AllowedGroups) == 0 && len(oidcConfig.AllowedUsers) == 0 {
			log.Println("OIDC: No allowed groups or users configured; anyone the provider authenticates can sign in with read-only access")
		}
	} else if oidcConfig != nil {
		log.Println("OIDC: Issuer, client ID and redirect URL are all required; OIDC authentication disabled")
	}

	username, password, created, err := userStore.EnsureAdminUser()
	if err != nil {
		return nil, fmt.Errorf("failed to ensure admin user: %w", err)
	}

	if created {
		log.Println("╔════════════════════════════════════════════════════════════════╗")
		log.Println("║                    ADMIN PASSWORD GENERATED                    ║")
		log.Println("╠════════════════════════════════════════════════════════════════╣")
		log.Printf("║  Username: %-51s ║\n", username)
		log.Printf("║  Password: %-51s ║\n", password)
		log.Println("╠════════════════════════════════════════════════════════════════╣")
		log.Println("║  This password will NOT be shown again!                        ║")
		log.Println("║  Save it now or reset it using --reset-admin-password flag     ║")
		log.Println("╚════════════════════════════════════════════════════════════════╝")
	} else {
		log.Println("Admin authentication enabled")
	}

	return m, nil
}

func (m *Manager) GenerateToken(username string, isAdmin bool) (string, error) {
	claims := &Claims{
		Username: username,
		IsAdmin:  isAdmin,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ID:        generateTokenID(),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(m.jwtSecret)
}

func (m *Manager) ValidateToken(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return m.jwtSecret, nil
	})
	if err != nil {
		return nil, err
	}

	if claims, ok := token.Claims.(*Claims); ok && token.Valid {
		return claims, nil
	}

	return nil, fmt.Errorf("invalid token")
}

func (m *Manager) ValidateCredentials(username, password string) bool {
	user, err := m.userStore.GetUser(username)
	if err != nil {
		return false
	}

	if !user.Enabled || user.AuthSource() != models.UserSourceLocal {
		return false
	}

	if !user.CheckPassword(password) {
		return false
	}

	_ = m.userStore.UpdateUserLastLogin(username)
	return true
}

func (m *Manager) GetUserAdmin(username string) bool {
	user, err := m.userStore.GetUser(username)
	if err != nil {
		return false
	}
	return user.IsAdmin
}

func (m *Manager) HandleAuthInfo(w http.ResponseWriter, r *http.Request) {
	backends := []map[string]string{
		{"id": "local", "name": "Local"},
	}
	if m.ldapConfig != nil {
		name := "LDAP"
		if m.ldapConfig.Host != "" {
			name = "LDAP (" + m.ldapConfig.Host + ")"
		}
		backends = append(backends, map[string]string{"id": "ldap", "name": name})
	}
	if m.oidc != nil {
		backends = append(backends, map[string]string{
			"id":   "oidc",
			"name": m.oidc.config.displayName(),
			"type": "redirect",
			"url":  "/api/oidc/login",
		})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "data": backends})
}

func (m *Manager) HandleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Invalid request body"})
		return
	}

	var authenticated bool
	var isAdmin bool
	failureMessage := "Invalid username or password"

	method := req.AuthMethod
	if method == "" {
		method = "local"
	}
	username := req.Username
	limiterKey := strings.ToLower(strings.TrimSpace(req.Username))
	addr := clientAddr(r)

	if wait, locked := m.limiter().check(limiterKey, addr); locked {
		seconds := retrySeconds(wait)
		log.Printf("Auth: Login for '%s' from %s refused, locked out for %ds", req.Username, addr, seconds)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", strconv.Itoa(seconds))
		w.WriteHeader(http.StatusTooManyRequests)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": fmt.Sprintf("Too many failed login attempts. Try again in %d seconds.", seconds)})
		return
	}

	switch method {
	case "ldap":
		if m.ldapConfig == nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "LDAP is not configured"})
			return
		}
		ldapOk, ldapAdmin, err := ldapAuthenticate(m.ldapConfig, req.Username, req.Password)
		if err != nil {
			log.Printf("LDAP: Error authenticating %s: %v", req.Username, err)
		}
		if ldapOk {
			username = strings.ToLower(req.Username)
			user, err := m.provisionExternalUser(username, models.UserSourceLDAP, ldapAdmin)
			if err != nil {
				log.Printf("LDAP: Login refused for %s: %v", username, err)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Login refused: " + err.Error()})
				return
			}
			authenticated = true
			isAdmin = user.IsAdmin
		}
	default:
		if m.ValidateCredentials(req.Username, req.Password) {
			user, err := m.userStore.GetUser(req.Username)
			if err == nil && user.TOTPEnabled {
				if strings.TrimSpace(req.Code) == "" {
					totpRequiredResponse(w)
					return
				}
				ok, usedRecovery := VerifyTOTP(user, req.Code)
				if !ok {
					failureMessage = "Invalid two-factor code"
					break
				}
				if usedRecovery {
					if err := m.userStore.UpdateUser(user.Username, user); err != nil {
						log.Printf("Auth: failed to consume recovery code for %s: %v", user.Username, err)
					}
					log.Printf("Auth: User '%s' signed in with a recovery code (%d left)", user.Username, RemainingRecoveryCodes(user))
				}
			}
			authenticated = true
			isAdmin = m.GetUserAdmin(req.Username)
		}
	}

	if !authenticated {
		m.limiter().fail(limiterKey, addr)
		log.Printf("Auth: Failed login for '%s' from %s", req.Username, addr)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": failureMessage})
		return
	}

	m.limiter().succeed(limiterKey)

	token, err := m.GenerateToken(username, isAdmin)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Failed to generate token"})
		return
	}

	log.Printf("Auth: User '%s' logged in", username)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"data": LoginResponse{
			Token:    token,
			Username: username,
			IsAdmin:  isAdmin,
		},
	})
}

func (m *Manager) authenticate(w http.ResponseWriter, r *http.Request) (*Claims, bool) {
	tokenString, ok := bearerToken(r)
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Authentication required"})
		return nil, false
	}

	claims, err := m.AuthenticateToken(tokenString)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": err.Error()})
		return nil, false
	}
	return claims, true
}

var (
	ErrInvalidToken = errors.New("Invalid or expired token")
	ErrUserDisabled = errors.New("User account disabled")
)

func (m *Manager) AuthenticateToken(tokenString string) (*Claims, error) {
	if IsAPISecret(tokenString) {
		return m.authenticateAPIToken(tokenString)
	}
	claims, err := m.ValidateToken(tokenString)
	if err != nil {
		return nil, ErrInvalidToken
	}
	user, err := m.userStore.GetUser(claims.Username)
	if err != nil || !user.Enabled {
		return nil, ErrUserDisabled
	}
	claims.IsAdmin = user.IsAdmin
	return claims, nil
}

type contextKey string

const usernameContextKey contextKey = "username"

func withUsername(r *http.Request, username string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), usernameContextKey, username))
}

func UsernameFromContext(ctx context.Context) string {
	username, _ := ctx.Value(usernameContextKey).(string)
	return username
}

func (m *Manager) JWTMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims, ok := m.authenticate(w, r)
		if !ok {
			return
		}
		next(w, withUsername(r, claims.Username))
	}
}

func (m *Manager) AdminMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims, ok := m.authenticate(w, r)
		if !ok {
			return
		}
		if !claims.IsAdmin {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Administrator privileges required"})
			return
		}
		next(w, withUsername(r, claims.Username))
	}
}

func (m *Manager) LDAPEnabled() bool {
	return m.ldapConfig != nil
}

func (m *Manager) LDAPHost() string {
	if m.ldapConfig != nil {
		return m.ldapConfig.Host
	}
	return ""
}

func generateTokenID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}
