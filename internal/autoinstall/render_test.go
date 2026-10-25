package autoinstall

import (
	"reflect"
	"testing"

	"bootimus/internal/models"
)

func TestRender(t *testing.T) {
	tests := []struct {
		name           string
		content        string
		vars           map[string]string
		want           string
		wantUnresolved []string
	}{
		{
			name:           "resolves known tokens",
			content:        "hostname: {{HOSTNAME}}\nmac: {{MAC}}\n",
			vars:           map[string]string{"HOSTNAME": "bench-1", "MAC": "aa:bb:cc:dd:ee:ff"},
			want:           "hostname: bench-1\nmac: aa:bb:cc:dd:ee:ff\n",
			wantUnresolved: []string{},
		},
		{
			name:           "empty value counts as resolved",
			content:        "name: {{CLIENT_NAME}}",
			vars:           map[string]string{"CLIENT_NAME": ""},
			want:           "name: ",
			wantUnresolved: []string{},
		},
		{
			name:           "unknown tokens stay literal and are listed once",
			content:        "{{HOSTNAME}} {{UNKNOWN}} {{VAR.role}} {{UNKNOWN}}",
			vars:           map[string]string{"HOSTNAME": "x"},
			want:           "x {{UNKNOWN}} {{VAR.role}} {{UNKNOWN}}",
			wantUnresolved: []string{"UNKNOWN", "VAR.role"},
		},
		{
			name:           "variable tokens",
			content:        "role={{VAR.role}} rack={{VAR.rack_id}}",
			vars:           map[string]string{"VAR.role": "worker", "VAR.rack_id": "7"},
			want:           "role=worker rack=7",
			wantUnresolved: []string{},
		},
		{
			name:           "lowercase and templating syntax is left alone",
			content:        "{{ .Values }} {{lower}} {{HOST-NAME}}",
			vars:           map[string]string{},
			want:           "{{ .Values }} {{lower}} {{HOST-NAME}}",
			wantUnresolved: []string{},
		},
		{
			name:           "no substitution inside values",
			content:        "{{A}}",
			vars:           map[string]string{"A": "{{B}}", "B": "c"},
			want:           "{{B}}",
			wantUnresolved: []string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, unresolved := Render(tt.content, tt.vars)
			if got != tt.want {
				t.Errorf("rendered = %q, want %q", got, tt.want)
			}
			if !reflect.DeepEqual(unresolved, tt.wantUnresolved) {
				t.Errorf("unresolved = %v, want %v", unresolved, tt.wantUnresolved)
			}
		})
	}
}

func TestContextVariables(t *testing.T) {
	image := &models.Image{Name: "Ubuntu 24.04", Filename: "ubuntu 24.04/live-server.iso"}
	group := &models.ClientGroup{Name: "lab-3", Variables: models.StringMap{"role": "worker", "rack": "3"}}
	client := &models.Client{Name: "bench-1", Variables: models.StringMap{"role": "control-plane"}}

	tests := []struct {
		name string
		ctx  Context
		want map[string]string
	}{
		{
			name: "server only",
			ctx:  Context{ServerAddr: "10.0.0.1", ServerPort: 8080},
			want: map[string]string{
				"MAC": "", "CLIENT_NAME": "", "HOSTNAME": "", "IP": "", "CLIENT_GROUP": "",
				"SERVER_ADDR": "10.0.0.1", "SERVER_PORT": "8080", "SERVER_URL": "http://10.0.0.1:8080",
			},
		},
		{
			name: "client overrides group variables",
			ctx:  Context{MAC: "aa:bb:cc:dd:ee:ff", ClientIP: "10.0.0.50", ServerAddr: "10.0.0.1", ServerPort: 8080, Image: image, Client: client, Group: group},
			want: map[string]string{
				"MAC": "aa:bb:cc:dd:ee:ff", "CLIENT_NAME": "bench-1", "HOSTNAME": "bench-1", "IP": "10.0.0.50", "CLIENT_GROUP": "lab-3",
				"SERVER_ADDR": "10.0.0.1", "SERVER_PORT": "8080", "SERVER_URL": "http://10.0.0.1:8080",
				"IMAGE_NAME": "Ubuntu 24.04", "IMAGE_FILENAME": "ubuntu 24.04/live-server.iso",
				"IMAGE_URL": "http://10.0.0.1:8080/isos/ubuntu%2024.04/live-server.iso",
				"VAR.role":  "control-plane", "VAR.rack": "3",
			},
		},
		{
			name: "group without client",
			ctx:  Context{ServerAddr: "10.0.0.1", ServerPort: 8080, Group: group},
			want: map[string]string{
				"MAC": "", "CLIENT_NAME": "", "HOSTNAME": "", "IP": "", "CLIENT_GROUP": "lab-3",
				"SERVER_ADDR": "10.0.0.1", "SERVER_PORT": "8080", "SERVER_URL": "http://10.0.0.1:8080",
				"VAR.role": "worker", "VAR.rack": "3",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.ctx.Variables()
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Variables() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidateVariableNames(t *testing.T) {
	tests := []struct {
		name    string
		vars    map[string]string
		wantErr bool
	}{
		{"nil", nil, false},
		{"valid", map[string]string{"role": "a", "RACK_7": "b", "x1": ""}, false},
		{"dot", map[string]string{"a.b": "x"}, true},
		{"space", map[string]string{"a b": "x"}, true},
		{"empty name", map[string]string{"": "x"}, true},
		{"braces", map[string]string{"{{X}}": "x"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateVariableNames(tt.vars)
			if (err != nil) != tt.wantErr {
				t.Errorf("error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
