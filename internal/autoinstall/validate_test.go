package autoinstall

import (
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		content  string
		wantErr  string
	}{
		{"yaml with placeholders", "default.yaml", "#cloud-config\nautoinstall:\n  version: 1\n  identity:\n    hostname: {{HOSTNAME}}\n    role: {{VAR.role}}\n", ""},
		{"yml multi document", "a.yml", "a: 1\n---\nb: 2\n", ""},
		{"yaml bad indentation", "default.yaml", "a:\n  b: 1\n c: 2\n", "invalid YAML: line "},
		{"yaml bare mapping value on first line", "default.yaml", "key: value: other\n", "invalid YAML: mapping values are not allowed"},
		{"yaml bare mapping value on later line", "default.yaml", "x: 1\nkey: value: other\n", "invalid YAML: line 2: mapping values"},
		{"xml well formed", "kiosk.xml", "<?xml version=\"1.0\"?>\n<unattend><ComputerName>{{HOSTNAME}}</ComputerName></unattend>\n", ""},
		{"xml unclosed tag", "kiosk.xml", "<unattend>\n<ComputerName>x\n</unattend>\n", "invalid XML: line 3"},
		{"json valid", "config.json", "{\"hostname\": \"{{HOSTNAME}}\", \"port\": {{SERVER_PORT}}}\n", ""},
		{"json trailing comma", "config.json", "{\n  \"a\": 1,\n}\n", "invalid JSON: line 3"},
		{"json trailing garbage", "config.json", "{}\n\nx", "invalid JSON: line 3"},
		{"other extensions are not checked", "server.cfg", "d-i :: ::: {{", ""},
		{"kickstart not checked", "ws.ks", "%packages\n@^minimal\n", ""},
		{"no extension not checked", "Makefile", "{{{", ""},
		{"empty yaml", "empty.yaml", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Validate(tt.filename, tt.content)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestValidateDummySubstitutionOnlyAffectsPlaceholders(t *testing.T) {
	if err := Validate("x.yaml", "list:\n  - {{A}}\n  - {{VAR.b_2}}\nmap: {key: {{C}}}\n"); err != nil {
		t.Errorf("placeholders in every YAML position should validate, got %v", err)
	}
	if err := Validate("x.yaml", "bad: {{not a placeholder}}\n"); err == nil {
		t.Error("a non-placeholder brace construct must still be reported as invalid YAML")
	}
}
