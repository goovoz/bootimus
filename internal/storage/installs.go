package storage

import (
	"time"

	"bootimus/internal/models"

	"gorm.io/gorm"
)

func createInstallRecord(db *gorm.DB, rec *models.InstallRecord) error {
	return db.Create(rec).Error
}

func updateInstallRecord(db *gorm.DB, rec *models.InstallRecord) error {
	return db.Save(rec).Error
}

func getInstallRecordByToken(db *gorm.DB, token string) (*models.InstallRecord, error) {
	var rec models.InstallRecord
	if err := db.Where("token = ?", token).First(&rec).Error; err != nil {
		return nil, err
	}
	return &rec, nil
}

func latestOpenInstallRecord(db *gorm.DB, mac, imageFilename string) (*models.InstallRecord, error) {
	var rec models.InstallRecord
	q := db.Where("mac_address = ? AND state IN ?", mac, []string{models.InstallStateBooted, models.InstallStateInstalling})
	if imageFilename != "" {
		q = q.Where("image_filename = ?", imageFilename)
	}
	if err := q.Order("id DESC").First(&rec).Error; err != nil {
		return nil, err
	}
	return &rec, nil
}

func listInstallRecords(db *gorm.DB, mac string, limit int) ([]models.InstallRecord, error) {
	if limit <= 0 {
		limit = 50
	}
	q := db.Model(&models.InstallRecord{})
	if mac != "" {
		q = q.Where("mac_address = ?", mac)
	}
	var recs []models.InstallRecord
	if err := q.Order("id DESC").Limit(limit).Find(&recs).Error; err != nil {
		return nil, err
	}
	return recs, nil
}

func listOpenInstallRecordsBefore(db *gorm.DB, cutoff time.Time) ([]models.InstallRecord, error) {
	var recs []models.InstallRecord
	err := db.Where("state IN ? AND booted_at < ?", []string{models.InstallStateBooted, models.InstallStateInstalling}, cutoff).
		Order("id ASC").Find(&recs).Error
	if err != nil {
		return nil, err
	}
	return recs, nil
}

func setClientInstallStatus(db *gorm.DB, mac, state, image string, at time.Time) error {
	return db.Model(&models.Client{}).Where("mac_address = ?", mac).Updates(map[string]interface{}{
		"install_state":      state,
		"install_image":      image,
		"install_updated_at": at,
	}).Error
}

func (s *SQLiteStore) CreateInstallRecord(rec *models.InstallRecord) error {
	return createInstallRecord(s.db, rec)
}

func (s *SQLiteStore) UpdateInstallRecord(rec *models.InstallRecord) error {
	return updateInstallRecord(s.db, rec)
}

func (s *SQLiteStore) GetInstallRecordByToken(token string) (*models.InstallRecord, error) {
	return getInstallRecordByToken(s.db, token)
}

func (s *SQLiteStore) LatestOpenInstallRecord(mac, imageFilename string) (*models.InstallRecord, error) {
	return latestOpenInstallRecord(s.db, mac, imageFilename)
}

func (s *SQLiteStore) ListInstallRecords(mac string, limit int) ([]models.InstallRecord, error) {
	return listInstallRecords(s.db, mac, limit)
}

func (s *SQLiteStore) ListOpenInstallRecordsBefore(cutoff time.Time) ([]models.InstallRecord, error) {
	return listOpenInstallRecordsBefore(s.db, cutoff)
}

func (s *SQLiteStore) SetClientInstallStatus(mac, state, image string, at time.Time) error {
	return setClientInstallStatus(s.db, mac, state, image, at)
}

func (s *PostgresStore) CreateInstallRecord(rec *models.InstallRecord) error {
	return createInstallRecord(s.db, rec)
}

func (s *PostgresStore) UpdateInstallRecord(rec *models.InstallRecord) error {
	return updateInstallRecord(s.db, rec)
}

func (s *PostgresStore) GetInstallRecordByToken(token string) (*models.InstallRecord, error) {
	return getInstallRecordByToken(s.db, token)
}

func (s *PostgresStore) LatestOpenInstallRecord(mac, imageFilename string) (*models.InstallRecord, error) {
	return latestOpenInstallRecord(s.db, mac, imageFilename)
}

func (s *PostgresStore) ListInstallRecords(mac string, limit int) ([]models.InstallRecord, error) {
	return listInstallRecords(s.db, mac, limit)
}

func (s *PostgresStore) ListOpenInstallRecordsBefore(cutoff time.Time) ([]models.InstallRecord, error) {
	return listOpenInstallRecordsBefore(s.db, cutoff)
}

func (s *PostgresStore) SetClientInstallStatus(mac, state, image string, at time.Time) error {
	return setClientInstallStatus(s.db, mac, state, image, at)
}
