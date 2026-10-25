package storage

import (
	"time"

	"bootimus/internal/models"

	"gorm.io/gorm"
)

const (
	DefaultBootLogDays          = 90
	DefaultInventoryHistoryDays = 90
	DefaultAuditLogDays         = 365
)

func getHousekeepingSettings(db *gorm.DB) (*models.HousekeepingSettings, error) {
	var settings models.HousekeepingSettings
	if err := db.First(&settings, 1).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return &models.HousekeepingSettings{
				ID:                   1,
				BootLogDays:          DefaultBootLogDays,
				InventoryHistoryDays: DefaultInventoryHistoryDays,
				AuditLogDays:         DefaultAuditLogDays,
			}, nil
		}
		return nil, err
	}
	return &settings, nil
}

func updateHousekeepingSettings(db *gorm.DB, settings *models.HousekeepingSettings) error {
	settings.ID = 1
	return db.Save(settings).Error
}

func recordHousekeepingRun(db *gorm.DB, status, report string) error {
	settings, err := getHousekeepingSettings(db)
	if err != nil {
		return err
	}
	now := time.Now()
	settings.LastRun = &now
	settings.LastStatus = status
	settings.LastReport = report
	return updateHousekeepingSettings(db, settings)
}

func deleteBootLogsBefore(db *gorm.DB, cutoff time.Time) (int64, error) {
	res := db.Where("created_at < ?", cutoff).Delete(&models.BootLog{})
	return res.RowsAffected, res.Error
}

func deleteInventoryHistoryBefore(db *gorm.DB, cutoff time.Time) (int64, error) {
	latest := db.Model(&models.HardwareInventory{}).Select("MAX(id)").Group("mac_address")
	res := db.Where("created_at < ? AND id NOT IN (?)", cutoff, latest).Delete(&models.HardwareInventory{})
	return res.RowsAffected, res.Error
}

func deleteDiscoveredClientsNotSeenSince(db *gorm.DB, cutoff time.Time) (int64, error) {
	var clients []models.Client
	err := db.Where("static = ? AND (enrollment_state = ? OR enrollment_state IS NULL)", false, models.EnrollmentStateUnmanaged).
		Where("(last_boot IS NOT NULL AND last_boot < ?) OR (last_boot IS NULL AND created_at < ?)", cutoff, cutoff).
		Find(&clients).Error
	if err != nil {
		return 0, err
	}
	var deleted int64
	for i := range clients {
		if err := hardDeleteClient(db, &clients[i]); err != nil {
			return deleted, err
		}
		deleted++
	}
	return deleted, nil
}
