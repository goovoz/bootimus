package storage

import (
	"errors"
	"os"
	"testing"

	"bootimus/internal/models"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type namedStore struct {
	name  string
	store Storage
}

func newTestPostgresStore(t *testing.T, dsn string) *PostgresStore {
	t.Helper()
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open PostgreSQL: %v", err)
	}
	store := &PostgresStore{db: db}
	if err := store.AutoMigrate(); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	return store
}

func TestCreateClientReportsExistingMAC(t *testing.T) {
	stores := []namedStore{{"sqlite", newTestStore(t)}}
	if dsn := os.Getenv("BOOTIMUS_TEST_POSTGRES_DSN"); dsn != "" {
		stores = append(stores, namedStore{"postgres", newTestPostgresStore(t, dsn)})
	} else {
		t.Log("BOOTIMUS_TEST_POSTGRES_DSN not set, testing SQLite only")
	}

	for _, tt := range stores {
		t.Run(tt.name, func(t *testing.T) {
			mac := "aa:bb:cc:dd:ee:10"
			if err := tt.store.DeleteClient(mac); err != nil {
				t.Fatalf("DeleteClient: %v", err)
			}
			t.Cleanup(func() { tt.store.DeleteClient(mac) })

			if err := tt.store.CreateClient(&models.Client{MACAddress: mac, Name: "first"}); err != nil {
				t.Fatalf("CreateClient: %v", err)
			}
			err := tt.store.CreateClient(&models.Client{MACAddress: mac, Name: "second"})
			if !errors.Is(err, ErrClientExists) {
				t.Fatalf("CreateClient with existing MAC: error = %v, want ErrClientExists", err)
			}

			client, err := tt.store.GetClient(mac)
			if err != nil {
				t.Fatalf("GetClient: %v", err)
			}
			if client.Name != "first" {
				t.Errorf("client name = %q, want first", client.Name)
			}
		})
	}
}
