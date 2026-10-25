package smb

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBindTarget(t *testing.T) {
	local := []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("192.168.1.10")}
	cases := []struct {
		name, iface, addr, want string
	}{
		{"bind interface wins", "eth0", "192.168.1.10", "eth0"},
		{"local server address", "", "192.168.1.10", "192.168.1.10"},
		{"address not on this host", "", "10.96.0.15", ""},
		{"hostname", "", "bootimus.lan", ""},
		{"no address", "", "", ""},
	}
	for _, c := range cases {
		if got := bindTarget(c.iface, c.addr, local); got != c.want {
			t.Errorf("%s: bindTarget(%q, %q) = %q, want %q", c.name, c.iface, c.addr, got, c.want)
		}
	}
}

func TestWriteConfigBinding(t *testing.T) {
	for _, c := range []struct {
		interfaces string
		wantBind   bool
	}{
		{"192.168.1.10", true},
		{"", false},
	} {
		m := &Manager{dataDir: t.TempDir(), interfaces: c.interfaces, port: 445, shares: map[string]string{}}
		if err := os.MkdirAll(m.smbDir(), 0755); err != nil {
			t.Fatal(err)
		}
		if err := m.writeConfig(); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(m.configPath())
		if err != nil {
			t.Fatal(err)
		}
		conf := string(data)
		hasBind := strings.Contains(conf, "bind interfaces only = yes")
		if hasBind != c.wantBind {
			t.Errorf("interfaces %q: bind interfaces only present = %v, want %v\n%s", c.interfaces, hasBind, c.wantBind, conf)
		}
		if c.wantBind && !strings.Contains(conf, "interfaces = "+c.interfaces+"\n") {
			t.Errorf("interfaces %q: missing interfaces line\n%s", c.interfaces, conf)
		}
		if !strings.Contains(conf, "log file = ") || !strings.Contains(conf, "\nsmb ports = 445\n") {
			t.Errorf("interfaces %q: surrounding config lines malformed\n%s", c.interfaces, conf)
		}
	}
}

func TestCheckPermissionSupport(t *testing.T) {
	dir := t.TempDir()
	if err := checkPermissionSupport(dir); err != nil {
		t.Fatalf("expected a native filesystem to pass: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".permcheck")); !os.IsNotExist(err) {
		t.Errorf("probe directory left behind: %v", err)
	}
}

func fakeSMBD(t *testing.T, script string) {
	t.Helper()
	bin := t.TempDir()
	path := filepath.Join(bin, "smbd")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestStartReportsImmediateExit(t *testing.T) {
	fakeSMBD(t, `mkdir -p "$(dirname "$4")/log"; echo "invalid permissions on directory msg.lock: has 0777 should be 0755" >> "$(dirname "$4")/log/smbd.log"; exit 1`)
	m := &Manager{dataDir: t.TempDir(), port: 44599, shares: map[string]string{}}
	err := m.Start()
	if err == nil {
		t.Fatal("expected Start to fail when smbd exits immediately")
	}
	if !strings.Contains(err.Error(), "exited immediately") || !strings.Contains(err.Error(), "has 0777 should be 0755") {
		t.Errorf("error should describe the early exit and include the smbd log tail: %v", err)
	}
	if m.Running() {
		t.Error("manager should not report running after smbd exits")
	}
	if m.Err() == nil {
		t.Error("Err should be set after a failed start")
	}
}

func TestStartKeepsRunningSMBD(t *testing.T) {
	fakeSMBD(t, `exec sleep 30`)
	m := &Manager{dataDir: t.TempDir(), port: 44599, shares: map[string]string{}}
	if err := m.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer m.Stop()
	if !m.Running() || m.Err() != nil {
		t.Errorf("expected running manager with no error, got running=%v err=%v", m.Running(), m.Err())
	}
}
