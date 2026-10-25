package ipmi

import (
	"context"
	"errors"
	"testing"
)

func TestOpenReportsMissingIpmitool(t *testing.T) {
	orig := lookPath
	lookPath = func(string) (string, error) { return "", errors.New("not found") }
	t.Cleanup(func() { lookPath = orig })
	if _, err := Open(context.Background(), "10.0.0.5", 0, "admin", "secret"); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("err = %v, want ErrNotInstalled", err)
	}
}

func TestBaseArgsUsesDefaultPortAndEnvPassword(t *testing.T) {
	got := baseArgs("10.0.0.5", 0, "admin")
	want := []string{"-I", "lanplus", "-H", "10.0.0.5", "-p", "623", "-U", "admin", "-E"}
	if len(got) != len(want) {
		t.Fatalf("args = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("args = %v, want %v", got, want)
		}
	}
	if custom := baseArgs("h", 6230, "u"); custom[5] != "6230" {
		t.Fatalf("custom port args = %v", custom)
	}
}
