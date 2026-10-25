package storage

import (
	"errors"

	"bootimus/internal/models"

	"gorm.io/gorm"
)

var ErrDiskImageInUse = errors.New("disk image has active jobs")

func createDiskImage(db *gorm.DB, image *models.DiskImage) error {
	return db.Create(image).Error
}

func getDiskImage(db *gorm.DB, id uint) (*models.DiskImage, error) {
	var image models.DiskImage
	if err := db.First(&image, id).Error; err != nil {
		return nil, err
	}
	return &image, nil
}

func getDiskImageByName(db *gorm.DB, name string) (*models.DiskImage, error) {
	var image models.DiskImage
	if err := db.Where("name = ?", name).First(&image).Error; err != nil {
		return nil, err
	}
	return &image, nil
}

func listDiskImages(db *gorm.DB) ([]*models.DiskImage, error) {
	var images []*models.DiskImage
	if err := db.Order("name ASC").Find(&images).Error; err != nil {
		return nil, err
	}
	return images, nil
}

func updateDiskImage(db *gorm.DB, image *models.DiskImage) error {
	return db.Save(image).Error
}

func deleteDiskImage(db *gorm.DB, id uint) error {
	var active int64
	if err := db.Model(&models.DiskJob{}).
		Where("disk_image_id = ? AND state IN ?", id, []string{models.DiskJobStatePending, models.DiskJobStateRunning}).
		Count(&active).Error; err != nil {
		return err
	}
	if active > 0 {
		return ErrDiskImageInUse
	}
	return db.Delete(&models.DiskImage{}, id).Error
}

func createDiskJob(db *gorm.DB, job *models.DiskJob) error {
	return db.Create(job).Error
}

func getDiskJob(db *gorm.DB, id uint) (*models.DiskJob, error) {
	var job models.DiskJob
	if err := db.Preload("DiskImage").First(&job, id).Error; err != nil {
		return nil, err
	}
	return &job, nil
}

func getDiskJobByToken(db *gorm.DB, token string) (*models.DiskJob, error) {
	var job models.DiskJob
	if err := db.Preload("DiskImage").Where("token = ?", token).First(&job).Error; err != nil {
		return nil, err
	}
	return &job, nil
}

func listDiskJobs(db *gorm.DB, mac string, limit int) ([]*models.DiskJob, error) {
	q := db.Preload("DiskImage").Order("id DESC")
	if mac != "" {
		q = q.Where("client_mac = ?", mac)
	}
	if limit > 0 {
		q = q.Limit(limit)
	}
	var jobs []*models.DiskJob
	if err := q.Find(&jobs).Error; err != nil {
		return nil, err
	}
	return jobs, nil
}

func activeDiskJobForClient(db *gorm.DB, mac string) (*models.DiskJob, error) {
	var job models.DiskJob
	err := db.Preload("DiskImage").
		Where("client_mac = ? AND state IN ?", mac, []string{models.DiskJobStatePending, models.DiskJobStateRunning}).
		Order("id ASC").First(&job).Error
	if err != nil {
		return nil, err
	}
	return &job, nil
}

func updateDiskJob(db *gorm.DB, job *models.DiskJob) error {
	return db.Omit("DiskImage").Save(job).Error
}

func (s *SQLiteStore) CreateDiskImage(image *models.DiskImage) error {
	return createDiskImage(s.db, image)
}
func (s *SQLiteStore) GetDiskImage(id uint) (*models.DiskImage, error) {
	return getDiskImage(s.db, id)
}
func (s *SQLiteStore) GetDiskImageByName(name string) (*models.DiskImage, error) {
	return getDiskImageByName(s.db, name)
}
func (s *SQLiteStore) ListDiskImages() ([]*models.DiskImage, error) { return listDiskImages(s.db) }
func (s *SQLiteStore) UpdateDiskImage(image *models.DiskImage) error {
	return updateDiskImage(s.db, image)
}
func (s *SQLiteStore) DeleteDiskImage(id uint) error               { return deleteDiskImage(s.db, id) }
func (s *SQLiteStore) CreateDiskJob(job *models.DiskJob) error     { return createDiskJob(s.db, job) }
func (s *SQLiteStore) GetDiskJob(id uint) (*models.DiskJob, error) { return getDiskJob(s.db, id) }
func (s *SQLiteStore) GetDiskJobByToken(token string) (*models.DiskJob, error) {
	return getDiskJobByToken(s.db, token)
}
func (s *SQLiteStore) ListDiskJobs(mac string, limit int) ([]*models.DiskJob, error) {
	return listDiskJobs(s.db, mac, limit)
}
func (s *SQLiteStore) ActiveDiskJobForClient(mac string) (*models.DiskJob, error) {
	return activeDiskJobForClient(s.db, mac)
}
func (s *SQLiteStore) UpdateDiskJob(job *models.DiskJob) error { return updateDiskJob(s.db, job) }

func (s *PostgresStore) CreateDiskImage(image *models.DiskImage) error {
	return createDiskImage(s.db, image)
}
func (s *PostgresStore) GetDiskImage(id uint) (*models.DiskImage, error) {
	return getDiskImage(s.db, id)
}
func (s *PostgresStore) GetDiskImageByName(name string) (*models.DiskImage, error) {
	return getDiskImageByName(s.db, name)
}
func (s *PostgresStore) ListDiskImages() ([]*models.DiskImage, error) { return listDiskImages(s.db) }
func (s *PostgresStore) UpdateDiskImage(image *models.DiskImage) error {
	return updateDiskImage(s.db, image)
}
func (s *PostgresStore) DeleteDiskImage(id uint) error               { return deleteDiskImage(s.db, id) }
func (s *PostgresStore) CreateDiskJob(job *models.DiskJob) error     { return createDiskJob(s.db, job) }
func (s *PostgresStore) GetDiskJob(id uint) (*models.DiskJob, error) { return getDiskJob(s.db, id) }
func (s *PostgresStore) GetDiskJobByToken(token string) (*models.DiskJob, error) {
	return getDiskJobByToken(s.db, token)
}
func (s *PostgresStore) ListDiskJobs(mac string, limit int) ([]*models.DiskJob, error) {
	return listDiskJobs(s.db, mac, limit)
}
func (s *PostgresStore) ActiveDiskJobForClient(mac string) (*models.DiskJob, error) {
	return activeDiskJobForClient(s.db, mac)
}
func (s *PostgresStore) UpdateDiskJob(job *models.DiskJob) error { return updateDiskJob(s.db, job) }
