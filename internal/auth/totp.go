package auth

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image/png"
	"math/big"
	"net/http"
	"strings"

	"bootimus/internal/models"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

const (
	totpIssuer         = "Bootimus"
	recoveryCodeCount  = 10
	recoveryCodeDigits = 10
)

var (
	ErrTOTPNotLocal     = errors.New("two-factor authentication is only available for local accounts")
	ErrTOTPNotSetUp     = errors.New("two-factor authentication has not been set up")
	ErrTOTPAlreadyOn    = errors.New("two-factor authentication is already enabled")
	ErrTOTPInvalidCode  = errors.New("invalid two-factor code")
	ErrTOTPNotEnabled   = errors.New("two-factor authentication is not enabled")
	ErrTOTPBadPassword  = errors.New("password is incorrect")
	ErrTOTPTokenCannot  = errors.New("API tokens cannot manage two-factor authentication")
	recoveryCodeCharset = "0123456789abcdefghjkmnpqrstuvwxyz"
)

func generateRecoveryCodes() ([]string, []string, error) {
	max := big.NewInt(int64(len(recoveryCodeCharset)))
	plain := make([]string, 0, recoveryCodeCount)
	hashed := make([]string, 0, recoveryCodeCount)
	for i := 0; i < recoveryCodeCount; i++ {
		var sb strings.Builder
		for j := 0; j < recoveryCodeDigits; j++ {
			if j == recoveryCodeDigits/2 {
				sb.WriteByte('-')
			}
			n, err := rand.Int(rand.Reader, max)
			if err != nil {
				return nil, nil, err
			}
			sb.WriteByte(recoveryCodeCharset[n.Int64()])
		}
		code := sb.String()
		plain = append(plain, code)
		hashed = append(hashed, hashRecoveryCode(code))
	}
	return plain, hashed, nil
}

func hashRecoveryCode(code string) string {
	normalised := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(code), " ", ""))
	sum := sha256.Sum256([]byte(normalised))
	return hex.EncodeToString(sum[:])
}

func encodeRecoveryCodes(hashes []string) string {
	b, _ := json.Marshal(hashes)
	return string(b)
}

func decodeRecoveryCodes(raw string) []string {
	var hashes []string
	if raw == "" {
		return hashes
	}
	_ = json.Unmarshal([]byte(raw), &hashes)
	return hashes
}

func BeginTOTP(user *models.User) (*otp.Key, string, error) {
	if user.AuthSource() != models.UserSourceLocal {
		return nil, "", ErrTOTPNotLocal
	}
	if user.TOTPEnabled {
		return nil, "", ErrTOTPAlreadyOn
	}
	key, err := totp.Generate(totp.GenerateOpts{Issuer: totpIssuer, AccountName: user.Username})
	if err != nil {
		return nil, "", err
	}
	img, err := key.Image(220, 220)
	if err != nil {
		return nil, "", err
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, "", err
	}
	user.TOTPSecret = key.Secret()
	return key, "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

func ConfirmTOTP(user *models.User, code string) ([]string, error) {
	if user.TOTPEnabled {
		return nil, ErrTOTPAlreadyOn
	}
	if user.TOTPSecret == "" {
		return nil, ErrTOTPNotSetUp
	}
	if !totp.Validate(strings.TrimSpace(code), user.TOTPSecret) {
		return nil, ErrTOTPInvalidCode
	}
	plain, hashed, err := generateRecoveryCodes()
	if err != nil {
		return nil, err
	}
	user.TOTPEnabled = true
	user.RecoveryCodes = encodeRecoveryCodes(hashed)
	return plain, nil
}

func ClearTOTP(user *models.User) {
	user.TOTPEnabled = false
	user.TOTPSecret = ""
	user.RecoveryCodes = ""
}

func VerifyTOTP(user *models.User, code string) (ok bool, consumedRecovery bool) {
	code = strings.TrimSpace(code)
	if code == "" || !user.TOTPEnabled {
		return false, false
	}
	if totp.Validate(code, user.TOTPSecret) {
		return true, false
	}
	hashes := decodeRecoveryCodes(user.RecoveryCodes)
	want := hashRecoveryCode(code)
	for i, h := range hashes {
		if h == want {
			user.RecoveryCodes = encodeRecoveryCodes(append(hashes[:i:i], hashes[i+1:]...))
			return true, true
		}
	}
	return false, false
}

func RemainingRecoveryCodes(user *models.User) int {
	return len(decodeRecoveryCodes(user.RecoveryCodes))
}

func (m *Manager) currentLocalUser(w http.ResponseWriter, r *http.Request) (*models.User, bool) {
	claims, ok := m.authenticate(w, r)
	if !ok {
		return nil, false
	}
	if IsTokenPrincipal(claims.Username) {
		writeJSONError(w, http.StatusForbidden, ErrTOTPTokenCannot.Error())
		return nil, false
	}
	user, err := m.userStore.GetUser(claims.Username)
	if err != nil {
		writeJSONError(w, http.StatusUnauthorized, "User not found")
		return nil, false
	}
	if user.AuthSource() != models.UserSourceLocal {
		writeJSONError(w, http.StatusBadRequest, ErrTOTPNotLocal.Error())
		return nil, false
	}
	return user, true
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": msg})
}

func writeJSONData(w http.ResponseWriter, message string, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "message": message, "data": data})
}

func (m *Manager) HandleTOTPSetup(w http.ResponseWriter, r *http.Request) {
	user, ok := m.currentLocalUser(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	key, qr, err := BeginTOTP(user)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := m.userStore.UpdateUser(user.Username, user); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSONData(w, "Scan the code with an authenticator app, then confirm with a code", map[string]interface{}{
		"otpauth_url": key.URL(),
		"secret":      key.Secret(),
		"qr_code":     qr,
	})
}

func (m *Manager) HandleTOTPEnable(w http.ResponseWriter, r *http.Request) {
	user, ok := m.currentLocalUser(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	var req struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	codes, err := ConfirmTOTP(user, req.Code)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := m.userStore.UpdateUser(user.Username, user); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSONData(w, "Two-factor authentication enabled. Store the recovery codes somewhere safe; they are shown once.", map[string]interface{}{
		"recovery_codes": codes,
	})
}

func (m *Manager) HandleTOTPDisable(w http.ResponseWriter, r *http.Request) {
	user, ok := m.currentLocalUser(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if !user.CheckPassword(req.Password) {
		writeJSONError(w, http.StatusForbidden, ErrTOTPBadPassword.Error())
		return
	}
	if !user.TOTPEnabled && user.TOTPSecret == "" {
		writeJSONError(w, http.StatusBadRequest, ErrTOTPNotEnabled.Error())
		return
	}
	ClearTOTP(user)
	if err := m.userStore.UpdateUser(user.Username, user); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSONData(w, "Two-factor authentication disabled", nil)
}

func totpRequiredResponse(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "totp_required", "totp_required": true, "message": "Enter the code from your authenticator app or a recovery code"})
}
