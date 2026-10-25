package database

import "bootimus/internal/models"

type UserStore interface {
	EnsureAdminUser() (username, password string, created bool, err error)
	ResetAdminPassword() (string, error)
	GetUser(username string) (*models.User, error)
	UpdateUserLastLogin(username string) error
	CreateUser(user *models.User) error
	UpdateUser(username string, user *models.User) error
}
