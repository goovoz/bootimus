package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"bootimus/internal/models"
	"bootimus/internal/storage"
)

const (
	EventBootStarted      = "boot.started"
	EventClientDiscovered = "client.discovered"
	EventInventoryUpdated = "client.inventory_updated"
	EventNodeApproved     = "node.approved"
	EventNodeRejected     = "node.rejected"
	EventInstallStarted   = "node.install_started"
	EventInstallCompleted = "node.install_completed"
	EventAdminAction      = "admin.action"
	EventDiskImaging      = "disk."
	EventInstallDone      = "install.completed"
	EventInstallFailed    = "install.failed"
)

type Event struct {
	Event      string            `json:"event"`
	Timestamp  time.Time         `json:"timestamp"`
	MAC        string            `json:"mac,omitempty"`
	ClientName string            `json:"client_name,omitempty"`
	Image      string            `json:"image,omitempty"`
	IP         string            `json:"ip,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

type Notifier struct {
	store  storage.Storage
	client *http.Client
}

func New(store storage.Storage) *Notifier {
	return &Notifier{
		store:  store,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

func (n *Notifier) Fire(ev Event) {
	if n == nil || n.store == nil {
		return
	}
	cfg, err := n.store.GetWebhookConfig()
	if err != nil || cfg == nil || !cfg.Enabled || cfg.URL == "" {
		return
	}
	if !eventEnabled(cfg, ev.Event) {
		return
	}
	if ev.Timestamp.IsZero() {
		ev.Timestamp = time.Now().UTC()
	}
	go n.deliver(cfg.URL, ev)
}

func eventEnabled(cfg *models.WebhookConfig, event string) bool {
	switch event {
	case EventBootStarted:
		return cfg.OnBootStarted
	case EventClientDiscovered:
		return cfg.OnClientDiscovered
	case EventInventoryUpdated:
		return cfg.OnInventoryUpdated
	case EventNodeApproved, EventNodeRejected, EventInstallStarted, EventInstallCompleted:
		return cfg.OnEnrollment
	case EventAdminAction:
		return cfg.OnAdminAction
	case EventInstallDone, EventInstallFailed:
		return cfg.OnInstall
	}
	if strings.HasPrefix(event, EventDiskImaging) {
		return cfg.OnDiskImaging
	}
	return false
}

func (n *Notifier) deliver(url string, ev Event) {
	body, err := json.Marshal(ev)
	if err != nil {
		log.Printf("webhook: marshal failed: %v", err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		log.Printf("webhook: build request failed: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "bootimus-webhook/1")
	resp, err := n.client.Do(req)
	if err != nil {
		log.Printf("webhook: POST %s failed: %v", url, err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		log.Printf("webhook: POST %s returned HTTP %d", url, resp.StatusCode)
		return
	}
	log.Printf("webhook: delivered %s to %s", ev.Event, url)
}
