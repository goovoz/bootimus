package housekeeping

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"sync"
	"time"

	"bootimus/internal/models"
)

const (
	StatusOK      = "ok"
	StatusPartial = "partial"
	StatusFailed  = "failed"

	DefaultInterval   = 24 * time.Hour
	DefaultStartDelay = 3 * time.Minute
)

var ErrDisabled = errors.New("task disabled")

type Store interface {
	GetHousekeepingSettings() (*models.HousekeepingSettings, error)
	RecordHousekeepingRun(status, report string) error
	DeleteBootLogsBefore(cutoff time.Time) (int64, error)
	DeleteInventoryHistoryBefore(cutoff time.Time) (int64, error)
	DeleteDiscoveredClientsNotSeenSince(cutoff time.Time) (int64, error)
	DeleteAuditLogsBefore(cutoff time.Time) (int64, error)
}

type Task interface {
	Name() string
	Run(ctx context.Context, settings *models.HousekeepingSettings) (int64, error)
}

type TaskResult struct {
	Name     string `json:"name"`
	Affected int64  `json:"affected"`
	Skipped  bool   `json:"skipped"`
	Error    string `json:"error,omitempty"`
}

type Report struct {
	StartedAt  time.Time    `json:"started_at"`
	DurationMs int64        `json:"duration_ms"`
	Status     string       `json:"status"`
	Tasks      []TaskResult `json:"tasks"`
}

func (r *Report) JSON() string {
	b, err := json.Marshal(r)
	if err != nil {
		return ""
	}
	return string(b)
}

type Runner struct {
	store      Store
	tasks      []Task
	interval   time.Duration
	startDelay time.Duration
	runMu      sync.Mutex
	stopOnce   sync.Once
	stop       chan struct{}
	wg         sync.WaitGroup
}

func New(store Store) *Runner {
	r := &Runner{
		store:      store,
		interval:   DefaultInterval,
		startDelay: DefaultStartDelay,
		stop:       make(chan struct{}),
	}
	r.Register(pruneBootLogs(store))
	r.Register(pruneInventoryHistory(store))
	r.Register(pruneDiscoveredClients(store))
	r.Register(pruneAuditLogs(store))
	return r
}

func (r *Runner) Register(t Task) {
	r.tasks = append(r.tasks, t)
}

func (r *Runner) Start() {
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		select {
		case <-r.stop:
			return
		case <-time.After(r.startDelay):
		}
		r.RunNow()
		ticker := time.NewTicker(r.interval)
		defer ticker.Stop()
		for {
			select {
			case <-r.stop:
				return
			case <-ticker.C:
				r.RunNow()
			}
		}
	}()
}

func (r *Runner) Stop() {
	r.stopOnce.Do(func() { close(r.stop) })
	r.wg.Wait()
}

func (r *Runner) RunNow() *Report {
	r.runMu.Lock()
	defer r.runMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	report := &Report{StartedAt: time.Now(), Status: StatusOK}
	settings, err := r.store.GetHousekeepingSettings()
	if err != nil {
		report.Status = StatusFailed
		report.Tasks = append(report.Tasks, TaskResult{Name: "settings", Error: err.Error()})
		r.finish(report)
		return report
	}

	failed := 0
	for _, t := range r.tasks {
		result := TaskResult{Name: t.Name()}
		affected, err := t.Run(ctx, settings)
		switch {
		case errors.Is(err, ErrDisabled):
			result.Skipped = true
		case err != nil:
			result.Error = err.Error()
			failed++
			log.Printf("housekeeping: %s failed: %v", t.Name(), err)
		default:
			result.Affected = affected
			if affected > 0 {
				log.Printf("housekeeping: %s removed %d record(s)", t.Name(), affected)
			}
		}
		report.Tasks = append(report.Tasks, result)
	}
	switch {
	case failed == 0:
	case failed == len(r.tasks):
		report.Status = StatusFailed
	default:
		report.Status = StatusPartial
	}
	r.finish(report)
	return report
}

func (r *Runner) finish(report *Report) {
	report.DurationMs = time.Since(report.StartedAt).Milliseconds()
	if err := r.store.RecordHousekeepingRun(report.Status, report.JSON()); err != nil {
		log.Printf("housekeeping: failed to record run: %v", err)
	}
}

func ParseReport(raw string) *Report {
	if raw == "" {
		return nil
	}
	var report Report
	if err := json.Unmarshal([]byte(raw), &report); err != nil {
		return nil
	}
	return &report
}
