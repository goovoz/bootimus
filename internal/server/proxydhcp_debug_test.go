package server

import (
	"encoding/json"
	"net/http"
	"testing"

	"bootimus/internal/proxydhcp"
)

func TestProxyDHCPEventsRouteWhenDisabled(t *testing.T) {
	env := newAccessEnv(t)
	code, body := env.do(http.MethodGet, "/api/proxydhcp/events", env.viewer)
	if code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", code, body)
	}
	var resp proxyDHCPDebugResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Enabled {
		t.Error("enabled must be false when proxyDHCP is not running")
	}
	if resp.Events == nil || len(resp.Events) != 0 {
		t.Errorf("events = %v, want an empty list", resp.Events)
	}
	if resp.Bootfile.BIOS != proxydhcp.DefaultBootfileBIOS {
		t.Errorf("bios bootfile = %q", resp.Bootfile.BIOS)
	}
}

func TestProxyDHCPEventsRouteListsRecordedEvents(t *testing.T) {
	env := newAccessEnv(t)
	env.server.proxyDHCPRecorder = proxydhcp.NewRecorder(10)
	env.server.proxyDHCPRecorder.Record(proxydhcp.Event{MAC: "aa:bb:cc:dd:ee:ff", Outcome: proxydhcp.OutcomeReplied})
	code, body := env.do(http.MethodGet, "/api/proxydhcp/events", env.admin)
	if code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", code, body)
	}
	var resp proxyDHCPDebugResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Events) != 1 || resp.Events[0].MAC != "aa:bb:cc:dd:ee:ff" {
		t.Errorf("events = %+v", resp.Events)
	}
	if code, _ := env.do(http.MethodPost, "/api/proxydhcp/events/clear", env.viewer); code != http.StatusForbidden {
		t.Errorf("viewer clear: status = %d, want 403", code)
	}
	if code, _ := env.do(http.MethodPost, "/api/proxydhcp/events/clear", env.admin); code != http.StatusOK {
		t.Errorf("admin clear: status = %d, want 200", code)
	}
	if len(env.server.proxyDHCPRecorder.Events()) != 0 {
		t.Error("clear did not empty the recorder")
	}
}
