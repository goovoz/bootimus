package auth

import (
	"context"
	"crypto/subtle"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"bootimus/internal/models"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/oauth2"
)

const (
	oidcStateCookie   = "bootimus_oidc"
	oidcCookiePath    = "/api/oidc/"
	oidcStateAudience = "bootimus-oidc-state"
	oidcStateLifetime = 10 * time.Minute
)

var errNotPermitted = errors.New("not in an allowed or admin group or user list")

type OIDCConfig struct {
	Issuer        string
	ClientID      string
	ClientSecret  string
	RedirectURL   string
	Scopes        []string
	UsernameClaim string
	GroupsClaim   string
	AdminGroups   []string
	AllowedGroups []string
	AdminUsers    []string
	AllowedUsers  []string
	DisplayName   string
	SkipVerify    bool
}

func (c *OIDCConfig) IsConfigured() bool {
	return c.Issuer != "" && c.ClientID != "" && c.RedirectURL != ""
}

func (c *OIDCConfig) displayName() string {
	if c.DisplayName != "" {
		return c.DisplayName
	}
	return "SSO"
}

func (c *OIDCConfig) scopes() []string {
	scopes := []string{oidc.ScopeOpenID}
	for _, s := range c.Scopes {
		if s != "" && s != oidc.ScopeOpenID {
			scopes = append(scopes, s)
		}
	}
	return scopes
}

func (c *OIDCConfig) usernameClaim() string {
	if c.UsernameClaim != "" {
		return c.UsernameClaim
	}
	return "preferred_username"
}

func (c *OIDCConfig) groupsClaim() string {
	if c.GroupsClaim != "" {
		return c.GroupsClaim
	}
	return "groups"
}

type oidcState struct {
	State    string `json:"state"`
	Nonce    string `json:"nonce"`
	Verifier string `json:"verifier"`
	jwt.RegisteredClaims
}

type oidcClient struct {
	config     *OIDCConfig
	httpClient *http.Client

	mu       sync.Mutex
	provider *oidc.Provider
}

func newOIDCClient(config *OIDCConfig) *oidcClient {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if config.SkipVerify {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return &oidcClient{
		config:     config,
		httpClient: &http.Client{Timeout: 15 * time.Second, Transport: transport},
	}
}

func (c *oidcClient) context(ctx context.Context) context.Context {
	return oidc.ClientContext(ctx, c.httpClient)
}

func (c *oidcClient) getProvider(ctx context.Context) (*oidc.Provider, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.provider != nil {
		return c.provider, nil
	}
	provider, err := oidc.NewProvider(c.context(ctx), c.config.Issuer)
	if err != nil {
		return nil, err
	}
	c.provider = provider
	return provider, nil
}

func (c *oidcClient) oauth2Config(provider *oidc.Provider) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     c.config.ClientID,
		ClientSecret: c.config.ClientSecret,
		RedirectURL:  c.config.RedirectURL,
		Endpoint:     provider.Endpoint(),
		Scopes:       c.config.scopes(),
	}
}

func (c *oidcClient) secureCookie(r *http.Request) bool {
	if r != nil && r.TLS != nil {
		return true
	}
	return strings.HasPrefix(strings.ToLower(c.config.RedirectURL), "https://")
}

func (c *oidcClient) authenticate(ctx context.Context, code string, state *oidcState) (string, bool, error) {
	provider, err := c.getProvider(ctx)
	if err != nil {
		return "", false, fmt.Errorf("provider discovery failed: %w", err)
	}
	ctx = c.context(ctx)

	token, err := c.oauth2Config(provider).Exchange(ctx, code, oauth2.VerifierOption(state.Verifier))
	if err != nil {
		return "", false, fmt.Errorf("code exchange failed: %w", err)
	}

	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return "", false, errors.New("token response did not include an ID token")
	}

	idToken, err := provider.Verifier(&oidc.Config{ClientID: c.config.ClientID}).Verify(ctx, rawIDToken)
	if err != nil {
		return "", false, fmt.Errorf("ID token verification failed: %w", err)
	}
	if subtle.ConstantTimeCompare([]byte(idToken.Nonce), []byte(state.Nonce)) != 1 {
		return "", false, errors.New("ID token nonce mismatch")
	}

	claims := map[string]interface{}{}
	if err := idToken.Claims(&claims); err != nil {
		return "", false, fmt.Errorf("failed to read ID token claims: %w", err)
	}

	_, hasGroups := claims[c.config.groupsClaim()]
	if claimString(claims, c.config.usernameClaim()) == "" || !hasGroups {
		c.mergeUserInfo(ctx, provider, token, idToken.Subject, claims)
	}

	username := strings.ToLower(claimString(claims, c.config.usernameClaim()))
	if username == "" {
		return "", false, fmt.Errorf("no %q claim in ID token or userinfo", c.config.usernameClaim())
	}
	if c.config.usernameClaim() == "email" && !claimTrue(claims, "email_verified") {
		return "", false, fmt.Errorf("email %s is not marked as verified by the provider", username)
	}

	groups := claimStrings(claims, c.config.groupsClaim())
	isAdmin := groupsIntersect(groups, c.config.AdminGroups) || userMatches(username, c.config.AdminUsers)
	restricted := len(c.config.AllowedGroups) > 0 || len(c.config.AllowedUsers) > 0
	if restricted && !isAdmin && !groupsIntersect(groups, c.config.AllowedGroups) && !userMatches(username, c.config.AllowedUsers) {
		return username, false, errNotPermitted
	}

	return username, isAdmin, nil
}

func claimTrue(claims map[string]interface{}, name string) bool {
	switch v := claims[name].(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(v, "true")
	}
	return false
}

func userMatches(username string, entries []string) bool {
	for _, entry := range entries {
		entry = strings.ToLower(strings.TrimSpace(entry))
		if entry == "" {
			continue
		}
		if strings.HasPrefix(entry, "@") {
			if strings.HasSuffix(username, entry) {
				return true
			}
		} else if username == entry {
			return true
		}
	}
	return false
}

func (c *oidcClient) mergeUserInfo(ctx context.Context, provider *oidc.Provider, token *oauth2.Token, subject string, claims map[string]interface{}) {
	if provider.UserInfoEndpoint() == "" {
		return
	}
	info, err := provider.UserInfo(ctx, oauth2.StaticTokenSource(token))
	if err != nil {
		log.Printf("OIDC: Failed to fetch userinfo: %v", err)
		return
	}
	if info.Subject != subject {
		log.Printf("OIDC: Ignoring userinfo for a different subject")
		return
	}
	extra := map[string]interface{}{}
	if err := info.Claims(&extra); err != nil {
		log.Printf("OIDC: Failed to read userinfo claims: %v", err)
		return
	}
	for k, v := range extra {
		if _, ok := claims[k]; !ok {
			claims[k] = v
		}
	}
}

func claimString(claims map[string]interface{}, name string) string {
	s, _ := claims[name].(string)
	return strings.TrimSpace(s)
}

func claimStrings(claims map[string]interface{}, name string) []string {
	switch v := claims[name].(type) {
	case string:
		return []string{v}
	case []interface{}:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func groupsIntersect(have, want []string) bool {
	for _, h := range have {
		for _, w := range want {
			if strings.EqualFold(strings.TrimSpace(h), strings.TrimSpace(w)) {
				return true
			}
		}
	}
	return false
}

func (m *Manager) OIDCEnabled() bool {
	return m.oidc != nil
}

func (m *Manager) HandleOIDCLogin(w http.ResponseWriter, r *http.Request) {
	if m.oidc == nil {
		http.NotFound(w, r)
		return
	}

	provider, err := m.oidc.getProvider(r.Context())
	if err != nil {
		log.Printf("OIDC: Provider discovery failed for %s: %v", m.oidc.config.Issuer, err)
		oidcFail(w, r, "Unable to reach the identity provider")
		return
	}

	state := &oidcState{
		State:    randomSecret(),
		Nonce:    randomSecret(),
		Verifier: oauth2.GenerateVerifier(),
		RegisteredClaims: jwt.RegisteredClaims{
			Audience:  jwt.ClaimStrings{oidcStateAudience},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(oidcStateLifetime)),
		},
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, state).SignedString(m.jwtSecret)
	if err != nil {
		oidcFail(w, r, "Sign-in failed")
		return
	}

	m.setOIDCCookie(w, r, signed, int(oidcStateLifetime.Seconds()))
	authURL := m.oidc.oauth2Config(provider).AuthCodeURL(state.State, oidc.Nonce(state.Nonce), oauth2.S256ChallengeOption(state.Verifier))
	http.Redirect(w, r, authURL, http.StatusFound)
}

func (m *Manager) HandleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	if m.oidc == nil {
		http.NotFound(w, r)
		return
	}

	m.setOIDCCookie(w, r, "", -1)
	query := r.URL.Query()

	if providerErr := query.Get("error"); providerErr != "" {
		log.Printf("OIDC: Provider returned error %q: %s", providerErr, query.Get("error_description"))
		oidcFail(w, r, "The identity provider did not complete the sign-in")
		return
	}

	state, err := m.readOIDCState(r)
	if err != nil || query.Get("state") == "" || subtle.ConstantTimeCompare([]byte(state.State), []byte(query.Get("state"))) != 1 {
		if err != nil {
			log.Printf("OIDC: Invalid sign-in state: %v", err)
		} else {
			log.Printf("OIDC: Sign-in state mismatch")
		}
		oidcFail(w, r, "Sign-in expired or was started elsewhere, please try again")
		return
	}

	username, isAdmin, err := m.oidc.authenticate(r.Context(), query.Get("code"), state)
	if errors.Is(err, errNotPermitted) {
		log.Printf("OIDC: Login refused for %s: %v", username, err)
		oidcFail(w, r, "Your account is not permitted to use Bootimus")
		return
	}
	if err != nil {
		log.Printf("OIDC: Authentication failed: %v", err)
		oidcFail(w, r, "Sign-in failed")
		return
	}

	user, err := m.provisionExternalUser(username, models.UserSourceOIDC, isAdmin)
	if err != nil {
		log.Printf("OIDC: Login refused for %s: %v", username, err)
		oidcFail(w, r, "Login refused: "+err.Error())
		return
	}

	token, err := m.GenerateToken(user.Username, user.IsAdmin)
	if err != nil {
		oidcFail(w, r, "Failed to generate token")
		return
	}

	log.Printf("Auth: User '%s' logged in via OIDC", user.Username)
	http.Redirect(w, r, "/#oidc_token="+url.QueryEscape(token), http.StatusFound)
}

func (m *Manager) setOIDCCookie(w http.ResponseWriter, r *http.Request, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     oidcStateCookie,
		Value:    value,
		Path:     oidcCookiePath,
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   m.oidc.secureCookie(r),
		SameSite: http.SameSiteLaxMode,
	})
}

func (m *Manager) readOIDCState(r *http.Request) (*oidcState, error) {
	cookie, err := r.Cookie(oidcStateCookie)
	if err != nil {
		return nil, err
	}
	state := &oidcState{}
	_, err = jwt.ParseWithClaims(cookie.Value, state, func(token *jwt.Token) (interface{}, error) {
		return m.jwtSecret, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}), jwt.WithAudience(oidcStateAudience), jwt.WithExpirationRequired())
	if err != nil {
		return nil, err
	}
	return state, nil
}

func oidcFail(w http.ResponseWriter, r *http.Request, message string) {
	http.Redirect(w, r, "/#oidc_error="+url.QueryEscape(message), http.StatusFound)
}
