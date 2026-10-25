package storage

import (
	"time"

	"bootimus/internal/models"

	"gorm.io/gorm"
)

type AuditLogFilter struct {
	Username string
	Path     string
	Since    time.Time
	Limit    int
	Offset   int
}

func createAuditLog(db *gorm.DB, entry *models.AuditLog) error {
	return db.Create(entry).Error
}

func listAuditLogs(db *gorm.DB, filter AuditLogFilter) ([]models.AuditLog, int64, error) {
	q := db.Model(&models.AuditLog{})
	if filter.Username != "" {
		q = q.Where("username = ?", filter.Username)
	}
	if filter.Path != "" {
		q = q.Where("path LIKE ?", "%"+filter.Path+"%")
	}
	if !filter.Since.IsZero() {
		q = q.Where("created_at >= ?", filter.Since)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}
	var entries []models.AuditLog
	if err := q.Order("id DESC").Limit(limit).Offset(filter.Offset).Find(&entries).Error; err != nil {
		return nil, 0, err
	}
	return entries, total, nil
}

func deleteAuditLogsBefore(db *gorm.DB, cutoff time.Time) (int64, error) {
	res := db.Where("created_at < ?", cutoff).Delete(&models.AuditLog{})
	return res.RowsAffected, res.Error
}

func (s *SQLiteStore) CreateAuditLog(entry *models.AuditLog) error {
	return createAuditLog(s.db, entry)
}

func (s *SQLiteStore) ListAuditLogs(filter AuditLogFilter) ([]models.AuditLog, int64, error) {
	return listAuditLogs(s.db, filter)
}

func (s *SQLiteStore) DeleteAuditLogsBefore(cutoff time.Time) (int64, error) {
	return deleteAuditLogsBefore(s.db, cutoff)
}

func (s *PostgresStore) CreateAuditLog(entry *models.AuditLog) error {
	return createAuditLog(s.db, entry)
}

func (s *PostgresStore) ListAuditLogs(filter AuditLogFilter) ([]models.AuditLog, int64, error) {
	return listAuditLogs(s.db, filter)
}

func (s *PostgresStore) DeleteAuditLogsBefore(cutoff time.Time) (int64, error) {
	return deleteAuditLogsBefore(s.db, cutoff)
}
