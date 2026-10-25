package server

import (
	"testing"

	"bootimus/internal/autoinstall"
	"bootimus/internal/models"
)

func TestResolveAutoInstallSourceOrder(t *testing.T) {
	lib, err := autoinstall.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []struct{ distro, name, content string }{
		{"ubuntu", "client.yaml", "client"},
		{"ubuntu", "group.yaml", "group"},
		{"ubuntu", "image.yaml", "image"},
		{"debian", "server.cfg", "preseed"},
	} {
		if err := lib.Write(f.distro, f.name, f.content); err != nil {
			t.Fatal(err)
		}
	}

	inlineImage := func(file string) *models.Image {
		return &models.Image{Filename: "ubuntu.iso", AutoInstallFile: file, AutoInstallEnabled: true, AutoInstallScript: "inline", AutoInstallScriptType: "kickstart"}
	}

	tests := []struct {
		name       string
		lib        *autoinstall.Library
		image      *models.Image
		client     *models.Client
		group      *models.ClientGroup
		want       string
		wantType   string
		wantSource string
		wantErr    bool
	}{
		{
			name:       "client beats group and image",
			lib:        lib,
			image:      inlineImage("ubuntu/image.yaml"),
			client:     &models.Client{MACAddress: "aa:bb:cc:dd:ee:01", AutoInstallFile: "ubuntu/client.yaml"},
			group:      &models.ClientGroup{Name: "lab", AutoInstallFile: "ubuntu/group.yaml"},
			want:       "client",
			wantType:   "autoinstall",
			wantSource: "client:aa:bb:cc:dd:ee:01",
		},
		{
			name:       "group beats image",
			lib:        lib,
			image:      inlineImage("ubuntu/image.yaml"),
			client:     &models.Client{MACAddress: "aa:bb:cc:dd:ee:01"},
			group:      &models.ClientGroup{Name: "lab", AutoInstallFile: "ubuntu/group.yaml"},
			want:       "group",
			wantType:   "autoinstall",
			wantSource: "group:lab",
		},
		{
			name:       "image beats inline",
			lib:        lib,
			image:      inlineImage("ubuntu/image.yaml"),
			client:     &models.Client{MACAddress: "aa:bb:cc:dd:ee:01"},
			want:       "image",
			wantType:   "autoinstall",
			wantSource: "image:ubuntu.iso",
		},
		{
			name:       "inline when no files attached",
			lib:        lib,
			image:      inlineImage(""),
			want:       "inline",
			wantType:   "kickstart",
			wantSource: "inline:ubuntu.iso",
		},
		{
			name:       "missing client file falls through to group",
			lib:        lib,
			image:      inlineImage("ubuntu/image.yaml"),
			client:     &models.Client{MACAddress: "aa:bb:cc:dd:ee:01", AutoInstallFile: "ubuntu/missing.yaml"},
			group:      &models.ClientGroup{Name: "lab", AutoInstallFile: "ubuntu/group.yaml"},
			want:       "group",
			wantType:   "autoinstall",
			wantSource: "group:lab",
		},
		{
			name:       "missing group file falls through to image",
			lib:        lib,
			image:      inlineImage("ubuntu/image.yaml"),
			client:     &models.Client{MACAddress: "aa:bb:cc:dd:ee:01"},
			group:      &models.ClientGroup{Name: "lab", AutoInstallFile: "ubuntu/missing.yaml"},
			want:       "image",
			wantType:   "autoinstall",
			wantSource: "image:ubuntu.iso",
		},
		{
			name:       "missing image file falls through to inline",
			lib:        lib,
			image:      inlineImage("ubuntu/missing.yaml"),
			want:       "inline",
			wantType:   "kickstart",
			wantSource: "inline:ubuntu.iso",
		},
		{
			name:       "type follows the chosen file extension",
			lib:        lib,
			image:      &models.Image{Filename: "debian.iso", AutoInstallFile: "debian/server.cfg"},
			want:       "preseed",
			wantType:   "preseed",
			wantSource: "image:debian.iso",
		},
		{
			name:       "inline type defaults to generic",
			lib:        lib,
			image:      &models.Image{Filename: "x.iso", AutoInstallEnabled: true, AutoInstallScript: "raw"},
			want:       "raw",
			wantType:   "generic",
			wantSource: "inline:x.iso",
		},
		{
			name:       "nil library still serves inline",
			image:      inlineImage("ubuntu/image.yaml"),
			client:     &models.Client{MACAddress: "aa:bb:cc:dd:ee:01", AutoInstallFile: "ubuntu/client.yaml"},
			want:       "inline",
			wantType:   "kickstart",
			wantSource: "inline:ubuntu.iso",
		},
		{
			name:    "inline disabled gives error",
			lib:     lib,
			image:   &models.Image{Filename: "x.iso", AutoInstallScript: "raw"},
			wantErr: true,
		},
		{
			name:    "nothing configured gives error",
			lib:     lib,
			image:   &models.Image{Filename: "x.iso"},
			client:  &models.Client{MACAddress: "aa:bb:cc:dd:ee:01"},
			group:   &models.ClientGroup{Name: "lab"},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content, scriptType, source, err := resolveAutoInstallSource(tt.lib, tt.image, tt.client, tt.group)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got content %q from %s", content, source)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if content != tt.want {
				t.Errorf("content = %q, want %q", content, tt.want)
			}
			if scriptType != tt.wantType {
				t.Errorf("type = %q, want %q", scriptType, tt.wantType)
			}
			if source != tt.wantSource {
				t.Errorf("source = %q, want %q", source, tt.wantSource)
			}
		})
	}
}
