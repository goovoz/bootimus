package storage

import (
	"time"

	"bootimus/internal/models"

	"gorm.io/gorm"
)

func listAPITokens(db *gorm.DB) ([]*models.APIToken, error) {
	var tokens []*models.APIToken
	if err := db.Order("id DESC").Find(&tokens).Error; err != nil {
		return nil, err
	}
	return tokens, nil
}

func getAPITokenByHash(db *gorm.DB, hash string) (*models.APIToken, error) {
	var token models.APIToken
	if err := db.Where("hash = ?", hash).First(&token).Error; err != nil {
		return nil, err
	}
	return &token, nil
}

func getAPIToken(db *gorm.DB, id uint) (*models.APIToken, error) {
	var token models.APIToken
	if err := db.First(&token, id).Error; err != nil {
		return nil, err
	}
	return &token, nil
}

func revokeAPIToken(db *gorm.DB, id uint) error {
	res := db.Model(&models.APIToken{}).Where("id = ?", id).Update("revoked", true)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func touchAPIToken(db *gorm.DB, id uint, at time.Time) error {
	return db.Model(&models.APIToken{}).Where("id = ?", id).Update("last_used_at", at).Error
}

func (s *SQLiteStore) ListAPITokens() ([]*models.APIToken, error) { return listAPITokens(s.db) }
func (s *SQLiteStore) CreateAPIToken(token *models.APIToken) error {
	return s.db.Create(token).Error
}
func (s *SQLiteStore) GetAPITokenByHash(hash string) (*models.APIToken, error) {
	return getAPITokenByHash(s.db, hash)
}
func (s *SQLiteStore) GetAPIToken(id uint) (*models.APIToken, error) { return getAPIToken(s.db, id) }
func (s *SQLiteStore) RevokeAPIToken(id uint) error                  { return revokeAPIToken(s.db, id) }
func (s *SQLiteStore) TouchAPIToken(id uint, at time.Time) error {
	return touchAPIToken(s.db, id, at)
}

func (s *PostgresStore) ListAPITokens() ([]*models.APIToken, error) { return listAPITokens(s.db) }
func (s *PostgresStore) CreateAPIToken(token *models.APIToken) error {
	return s.db.Create(token).Error
}
func (s *PostgresStore) GetAPITokenByHash(hash string) (*models.APIToken, error) {
	return getAPITokenByHash(s.db, hash)
}
func (s *PostgresStore) GetAPIToken(id uint) (*models.APIToken, error) { return getAPIToken(s.db, id) }
func (s *PostgresStore) RevokeAPIToken(id uint) error                  { return revokeAPIToken(s.db, id) }
func (s *PostgresStore) TouchAPIToken(id uint, at time.Time) error {
	return touchAPIToken(s.db, id, at)
}
