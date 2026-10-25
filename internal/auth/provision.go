package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"regexp"

	"bootimus/internal/models"
)

var (
	errInvalidUsername = errors.New("username contains unsupported characters")
	errSourceConflict  = errors.New("a user with this name already exists from a different source")
	errUserDisabled    = errors.New("user account is disabled")
)

var validExternalUsername = regexp.MustCompile(`^[A-Za-z0-9._@+-]{1,128}$`)

func (m *Manager) provisionExternalUser(username, source string, isAdmin bool) (*models.User, error) {
	if !validExternalUsername.MatchString(username) {
		return nil, errInvalidUsername
	}

	user, err := m.userStore.GetUser(username)
	if err != nil {
		user = &models.User{
			Username: username,
			Source:   source,
			IsAdmin:  isAdmin,
			Enabled:  true,
		}
		if err := user.SetPassword(randomSecret()); err != nil {
			return nil, fmt.Errorf("failed to set password: %w", err)
		}
		if err := m.userStore.CreateUser(user); err != nil {
			return nil, fmt.Errorf("failed to create user: %w", err)
		}
		log.Printf("Auth: Created %s user '%s' (admin=%v)", source, username, isAdmin)
	} else {
		if user.AuthSource() != source {
			log.Printf("Auth: Refusing %s login for '%s': existing account is a %s user; delete it to let it be recreated from %s", source, username, user.AuthSource(), source)
			return nil, errSourceConflict
		}
		if !user.Enabled {
			return nil, errUserDisabled
		}
		if user.IsAdmin != isAdmin {
			user.IsAdmin = isAdmin
			if err := m.userStore.UpdateUser(username, user); err != nil {
				return nil, fmt.Errorf("failed to update user: %w", err)
			}
			log.Printf("Auth: Updated %s user '%s' (admin=%v)", source, username, isAdmin)
		}
	}

	_ = m.userStore.UpdateUserLastLogin(username)
	return user, nil
}

func randomSecret() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}
