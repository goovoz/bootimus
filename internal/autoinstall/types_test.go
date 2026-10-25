package autoinstall

import (
	"strings"
	"testing"
)

func TestFileType(t *testing.T) {
	cases := []struct {
		distro   string
		filename string
		want     string
	}{
		{"windows", "kiosk.xml", TypeAutounattend},
		{"windows7", "legacy.XML", TypeAutounattend},
		{"opensuse", "leap.xml", TypeAutoYaST},
		{"opensuse", "tumbleweed.json", TypeAgama},
		{"opensuse", "tumbleweed.jsonnet", TypeAgama},
		{"ubuntu", "notes.json", TypeGeneric},
		{"debian", "server.cfg", TypePreseed},
		{"rocky", "workstation.ks", TypeKickstart},
		{"ubuntu", "default.yaml", TypeAutoinstall},
		{"ubuntu", "default.yml", TypeAutoinstall},
		{"coreos", "worker.ign", TypeIgnition},
		{"flatcar", "worker.ign", TypeIgnition},
		{"proxmox", "answer.toml", TypeProxmox},
		{"debian", "answer.toml", TypeGeneric},
		{"arch", "install.sh", TypeGeneric},
		{"", "noext", TypeGeneric},
	}
	for _, c := range cases {
		if got := FileType(c.distro, c.filename); got != c.want {
			t.Errorf("FileType(%q, %q) = %q, want %q", c.distro, c.filename, got, c.want)
		}
	}
}

func TestPathType(t *testing.T) {
	cases := []struct {
		rel  string
		want string
	}{
		{"windows/kiosk.xml", TypeAutounattend},
		{"opensuse/leap.xml", TypeAutoYaST},
		{"proxmox/answer.toml", TypeProxmox},
		{"orphan.xml", TypeAutounattend},
		{`windows\kiosk.xml`, TypeAutounattend},
	}
	for _, c := range cases {
		if got := PathType(c.rel); got != c.want {
			t.Errorf("PathType(%q) = %q, want %q", c.rel, got, c.want)
		}
	}
}

func TestContentType(t *testing.T) {
	cases := []struct {
		scriptType string
		want       string
	}{
		{TypeAutounattend, "application/xml; charset=utf-8"},
		{TypeAutoYaST, "application/xml; charset=utf-8"},
		{TypeAgama, "application/json; charset=utf-8"},
		{TypeIgnition, "application/json; charset=utf-8"},
		{TypeProxmox, "application/toml; charset=utf-8"},
		{TypeAutoinstall, "text/yaml; charset=utf-8"},
		{TypeKickstart, "text/plain; charset=utf-8"},
		{TypeGeneric, "text/plain; charset=utf-8"},
	}
	for _, c := range cases {
		if got := ContentType(c.scriptType); got != c.want {
			t.Errorf("ContentType(%q) = %q, want %q", c.scriptType, got, c.want)
		}
	}
}

func TestProxmoxInterfaceMACs(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		want    []string
		wantErr bool
	}{
		{
			name: "two interfaces",
			body: `{"$schema":{"version":"1.0"},"product":{"fullname":"Proxmox VE","product":"pve","enable_btrfs":true},` +
				`"iso":{"release":"9.1","isorelease":"1"},"dmi":{"baseboard":{},"chassis":{},"system":{}},` +
				`"network_interfaces":[{"link":"eno1","mac":"AA:BB:CC:DD:EE:01"},{"link":"eno2","mac":"aa-bb-cc-dd-ee-02"}]}`,
			want: []string{"aa:bb:cc:dd:ee:01", "aa:bb:cc:dd:ee:02"},
		},
		{
			name:    "no interfaces",
			body:    `{"network_interfaces":[]}`,
			wantErr: true,
		},
		{
			name:    "not json",
			body:    `product = "pve"`,
			wantErr: true,
		},
	}
	for _, c := range cases {
		got, err := ProxmoxInterfaceMACs(strings.NewReader(c.body))
		if c.wantErr {
			if err == nil {
				t.Errorf("%s: expected error, got %v", c.name, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: unexpected error: %v", c.name, err)
			continue
		}
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
