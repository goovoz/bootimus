package storage

import (
	"bootimus/internal/models"

	"gorm.io/gorm"
)

func getReplicaState(db *gorm.DB) (*models.ReplicaState, error) {
	var state models.ReplicaState
	if err := db.First(&state, 1).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return &models.ReplicaState{ID: 1}, nil
		}
		return nil, err
	}
	return &state, nil
}

func saveReplicaState(db *gorm.DB, state *models.ReplicaState) error {
	state.ID = 1
	return db.Save(state).Error
}

func (s *SQLiteStore) GetReplicaState() (*models.ReplicaState, error) {
	return getReplicaState(s.db)
}

func (s *SQLiteStore) SaveReplicaState(state *models.ReplicaState) error {
	return saveReplicaState(s.db, state)
}

func (s *PostgresStore) GetReplicaState() (*models.ReplicaState, error) {
	return getReplicaState(s.db)
}

func (s *PostgresStore) SaveReplicaState(state *models.ReplicaState) error {
	return saveReplicaState(s.db, state)
}
