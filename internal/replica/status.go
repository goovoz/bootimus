package replica

import (
	"encoding/json"
	"time"
)

type KindResult struct {
	Name       string `json:"name"`
	Count      int    `json:"count"`
	Changed    int    `json:"changed"`
	Removed    int    `json:"removed"`
	Skipped    bool   `json:"skipped"`
	Note       string `json:"note,omitempty"`
	Error      string `json:"error,omitempty"`
	DurationMs int64  `json:"duration_ms"`
}

type Status struct {
	PrimaryURL      string       `json:"primary_url"`
	SyncISOs        bool         `json:"sync_isos"`
	IntervalSeconds int          `json:"interval_seconds"`
	Running         bool         `json:"running"`
	LastRun         *time.Time   `json:"last_run,omitempty"`
	LastSuccess     *time.Time   `json:"last_success,omitempty"`
	LastError       string       `json:"last_error,omitempty"`
	Kinds           []KindResult `json:"kinds"`
}

func (s Status) report() string {
	b, err := json.Marshal(s)
	if err != nil {
		return ""
	}
	return string(b)
}

func parseReport(raw string) (Status, bool) {
	var s Status
	if raw == "" {
		return s, false
	}
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return Status{}, false
	}
	return s, true
}
