package storage

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"bootimus/internal/models"

	"gorm.io/gorm"
)

var sanURISchemes = map[string]bool{
	"iscsi": true,
	"aoe":   true,
	"http":  true,
	"https": true,
	"fcp":   true,
}

func ValidateSANTarget(t *models.SANTarget) error {
	t.Name = strings.TrimSpace(t.Name)
	t.URI = strings.TrimSpace(t.URI)
	t.InitiatorIQN = strings.TrimSpace(t.InitiatorIQN)
	if t.Name == "" {
		return errors.New("SAN target name is required")
	}
	if t.URI == "" {
		return errors.New("SAN target URI is required")
	}
	scheme, rest, found := strings.Cut(t.URI, ":")
	scheme = strings.ToLower(scheme)
	if !found || rest == "" || !sanURISchemes[scheme] {
		return fmt.Errorf("SAN target URI must start with one of iscsi:, aoe:, http:, https: or fcp:")
	}
	if strings.ContainsAny(t.URI, " \t\r\n") {
		return errors.New("SAN target URI must not contain whitespace")
	}
	if scheme == "http" || scheme == "https" {
		if _, err := url.Parse(t.URI); err != nil {
			return fmt.Errorf("SAN target URI is not a valid URL: %w", err)
		}
	}
	for _, v := range []string{t.InitiatorIQN, t.CHAPUsername, t.CHAPPassword, t.ReverseCHAPUsername, t.ReverseCHAPPassword} {
		if strings.ContainsAny(v, "\r\n") {
			return errors.New("SAN target fields must not contain line breaks")
		}
	}
	if (t.CHAPPassword != "" && t.CHAPUsername == "") || (t.ReverseCHAPPassword != "" && t.ReverseCHAPUsername == "") {
		return errors.New("CHAP passwords need a matching username")
	}
	return nil
}

func listSANTargets(db *gorm.DB) ([]*models.SANTarget, error) {
	var targets []*models.SANTarget
	if err := db.Order("name ASC").Find(&targets).Error; err != nil {
		return nil, err
	}
	return targets, nil
}

func getSANTarget(db *gorm.DB, id uint) (*models.SANTarget, error) {
	var target models.SANTarget
	if err := db.First(&target, id).Error; err != nil {
		return nil, err
	}
	return &target, nil
}

var ErrSANTargetExists = errors.New("a SAN target with that name already exists")

func translateSANError(db *gorm.DB, err error) error {
	if err == nil {
		return nil
	}
	if translator, ok := db.Dialector.(gorm.ErrorTranslator); ok && errors.Is(translator.Translate(err), gorm.ErrDuplicatedKey) {
		return ErrSANTargetExists
	}
	return err
}

func createSANTarget(db *gorm.DB, target *models.SANTarget) error {
	if err := ValidateSANTarget(target); err != nil {
		return err
	}
	return translateSANError(db, db.Create(target).Error)
}

func updateSANTarget(db *gorm.DB, id uint, target *models.SANTarget) error {
	if err := ValidateSANTarget(target); err != nil {
		return err
	}
	target.ID = id
	return translateSANError(db, db.Model(&models.SANTarget{}).Where("id = ?", id).
		Select("Name", "Description", "URI", "InitiatorIQN", "CHAPUsername", "CHAPPassword", "ReverseCHAPUsername", "ReverseCHAPPassword", "UpdatedAt").
		Updates(target).Error)
}

func deleteSANTarget(db *gorm.DB, id uint) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.Client{}).Where("san_target_id = ?", id).Updates(map[string]interface{}{"san_target_id": nil, "boot_from_san": false}).Error; err != nil {
			return err
		}
		if err := tx.Model(&models.ClientGroup{}).Where("san_target_id = ?", id).Updates(map[string]interface{}{"san_target_id": nil, "boot_from_san": false}).Error; err != nil {
			return err
		}
		if err := tx.Model(&models.Client{}).Where("next_boot_image = ?", fmt.Sprintf("%s%d", models.NextBootSANPrefix, id)).Update("next_boot_image", "").Error; err != nil {
			return err
		}
		return tx.Delete(&models.SANTarget{}, id).Error
	})
}

func countSANTargetUsers(db *gorm.DB, id uint) (clients int64, groups int64, err error) {
	if err = db.Model(&models.Client{}).Where("san_target_id = ?", id).Count(&clients).Error; err != nil {
		return
	}
	err = db.Model(&models.ClientGroup{}).Where("san_target_id = ?", id).Count(&groups).Error
	return
}

func (s *SQLiteStore) ListSANTargets() ([]*models.SANTarget, error) {
	return listSANTargets(s.db)
}

func (s *SQLiteStore) GetSANTarget(id uint) (*models.SANTarget, error) {
	return getSANTarget(s.db, id)
}

func (s *SQLiteStore) CreateSANTarget(target *models.SANTarget) error {
	return createSANTarget(s.db, target)
}

func (s *SQLiteStore) UpdateSANTarget(id uint, target *models.SANTarget) error {
	return updateSANTarget(s.db, id, target)
}

func (s *SQLiteStore) DeleteSANTarget(id uint) error {
	return deleteSANTarget(s.db, id)
}

func (s *SQLiteStore) CountSANTargetUsers(id uint) (int64, int64, error) {
	return countSANTargetUsers(s.db, id)
}

func (s *PostgresStore) ListSANTargets() ([]*models.SANTarget, error) {
	return listSANTargets(s.db)
}

func (s *PostgresStore) GetSANTarget(id uint) (*models.SANTarget, error) {
	return getSANTarget(s.db, id)
}

func (s *PostgresStore) CreateSANTarget(target *models.SANTarget) error {
	return createSANTarget(s.db, target)
}

func (s *PostgresStore) UpdateSANTarget(id uint, target *models.SANTarget) error {
	return updateSANTarget(s.db, id, target)
}

func (s *PostgresStore) DeleteSANTarget(id uint) error {
	return deleteSANTarget(s.db, id)
}

func (s *PostgresStore) CountSANTargetUsers(id uint) (int64, int64, error) {
	return countSANTargetUsers(s.db, id)
}
