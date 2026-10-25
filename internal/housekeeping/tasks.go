package housekeeping

import (
	"context"
	"time"

	"bootimus/internal/models"
)

type pruneTask struct {
	name   string
	days   func(settings *models.HousekeepingSettings) int
	delete func(cutoff time.Time) (int64, error)
}

func (t *pruneTask) Name() string { return t.name }

func (t *pruneTask) Run(ctx context.Context, settings *models.HousekeepingSettings) (int64, error) {
	days := t.days(settings)
	if days <= 0 {
		return 0, ErrDisabled
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return t.delete(time.Now().AddDate(0, 0, -days))
}

func pruneBootLogs(store Store) Task {
	return &pruneTask{
		name:   "boot_logs",
		days:   func(s *models.HousekeepingSettings) int { return s.BootLogDays },
		delete: store.DeleteBootLogsBefore,
	}
}

func pruneInventoryHistory(store Store) Task {
	return &pruneTask{
		name:   "inventory_history",
		days:   func(s *models.HousekeepingSettings) int { return s.InventoryHistoryDays },
		delete: store.DeleteInventoryHistoryBefore,
	}
}

func pruneDiscoveredClients(store Store) Task {
	return &pruneTask{
		name:   "discovered_clients",
		days:   func(s *models.HousekeepingSettings) int { return s.DiscoveredClientDays },
		delete: store.DeleteDiscoveredClientsNotSeenSince,
	}
}

func pruneAuditLogs(store Store) Task {
	return &pruneTask{
		name:   "audit_logs",
		days:   func(s *models.HousekeepingSettings) int { return s.AuditLogDays },
		delete: store.DeleteAuditLogsBefore,
	}
}
