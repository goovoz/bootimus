package remotefetch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMirrors(t *testing.T) {
	got := Mirrors("https://raw.githubusercontent.com/garybowers/bootimus/main/distro-profiles.json")
	if len(got) != 3 {
		t.Fatalf("expected 3 mirrors, got %d: %v", len(got), got)
	}
	want := "https://cdn.jsdelivr.net/gh/garybowers/bootimus@main/distro-profiles.json"
	if got[0] != want {
		t.Errorf("first mirror = %q, want %q", got[0], want)
	}
	if Mirrors("https://example.com/a/b/c/d") != nil {
		t.Error("non-raw.githubusercontent host should yield no mirrors")
	}
	if Mirrors("https://raw.githubusercontent.com/owner/repo") != nil {
		t.Error("too few path segments should yield no mirrors")
	}
}

func TestWithMirrorsKeepsPrimaryFirst(t *testing.T) {
	primary := "https://raw.githubusercontent.com/o/r/main/x.json"
	got := WithMirrors(primary)
	if len(got) != 4 || got[0] != primary {
		t.Fatalf("unexpected list: %v", got)
	}
}

func TestGetFallsBackToNextSource(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer bad.Close()
	payload := "{\"version\":\"x\"}"
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(payload))
	}))
	defer good.Close()

	body, used, err := Get(context.Background(), nil, []string{bad.URL + "/p.json", good.URL + "/p.json"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if used != good.URL+"/p.json" {
		t.Errorf("used = %q, want the second source", used)
	}
	if string(body) != payload {
		t.Errorf("body = %q", body)
	}
}

func TestGetReportsEverySource(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer bad.Close()

	_, _, err := Get(context.Background(), nil, []string{bad.URL + "/a.json", bad.URL + "/b.json"})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "HTTP 404") {
		t.Errorf("error should mention the status: %v", err)
	}
}
