package autoinstall

// [bootimus-fork L1] fork-patch: autoinstall-generator 的回归测试。
// 与实现同生共死：上游若自带模板生成能力，本文件连同 generate.go 一并删除。

import (
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func fillValues(t *testing.T, tpl Template) map[string]string {
	t.Helper()
	values := make(map[string]string, len(tpl.Vars))
	for _, v := range tpl.Vars {
		if !v.Required {
			continue
		}
		if v.Type == "password" {
			values[v.Name] = "Sup3rSecret!"
			continue
		}
		if v.Default == "" {
			values[v.Name] = "value-" + v.Name
		}
	}
	return values
}

func TestTemplatesAreEmbedded(t *testing.T) {
	if len(Templates()) != 4 {
		t.Fatalf("expected 4 templates, got %d", len(Templates()))
	}
	for _, tpl := range Templates() {
		if resourceName(tpl.ID) == "" {
			t.Errorf("%s: no body resource", tpl.ID)
			continue
		}
		if _, err := templateFiles.ReadFile(tpl.body); err != nil {
			t.Errorf("%s: body not embedded: %v", tpl.ID, err)
		}
		if tpl.Format == "" || tpl.Filename == "" || tpl.Distro == "" {
			t.Errorf("%s: incomplete metadata", tpl.ID)
		}
		for _, v := range tpl.Vars {
			if v.Name == "" || v.Label == "" || v.Type == "" {
				t.Errorf("%s: incomplete var %+v", tpl.ID, v)
			}
		}
	}
}

func TestEveryTemplateRenders(t *testing.T) {
	markers := map[string][]string{
		"debian-preseed":       {"d-i passwd/username string admin", "d-i netcfg/get_hostname string bootimus-node"},
		"rhel-kickstart":       {"rootpw --plaintext", "%packages", "autopart --type=lvm"},
		"ubuntu-autoinstall":   {"autoinstall:", "hostname: bootimus-node", "layout:"},
		"windows-autounattend": {"<ComputerName>BOOTIMUS-PC</ComputerName>", "<AcceptEula>true</AcceptEula>"},
	}
	for _, tpl := range Templates() {
		out, err := Render(tpl.ID, fillValues(t, tpl))
		if err != nil {
			t.Fatalf("%s: render failed: %v", tpl.ID, err)
		}
		for _, want := range markers[tpl.ID] {
			if !strings.Contains(out, want) {
				t.Errorf("%s: output missing %q\n%s", tpl.ID, want, out)
			}
		}
		if err := Validate(tpl.Format, out); err != nil {
			t.Errorf("%s: produced invalid output: %v", tpl.ID, err)
		}
	}
}

func TestRenderHonoursValues(t *testing.T) {
	out, err := Render("rhel-kickstart", map[string]string{
		"hostname":      "node-42",
		"username":      "ops",
		"password":      "hunter2hunter2",
		"root_password": "rootpw123456",
		"partition":     "plain",
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{
		"network --bootproto=dhcp --device=link --activate --hostname=node-42",
		"user --name=ops",
		"rootpw --plaintext rootpw123456",
		"autopart --type=plain",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q\n%s", want, out)
		}
	}
}

func TestRenderRootPasswordFallsBackToUserPassword(t *testing.T) {
	out, err := Render("debian-preseed", map[string]string{"password": "only-one-pass"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(out, "d-i passwd/root-password password only-one-pass") {
		t.Errorf("root password did not fall back to the user password:\n%s", out)
	}
	if strings.Contains(out, "d-i passwd/root-login boolean true") {
		t.Errorf("root login should default to disabled:\n%s", out)
	}
}

func TestRenderRejectsMissingRequired(t *testing.T) {
	_, err := Render("ubuntu-autoinstall", map[string]string{"hostname": "x"})
	if err == nil {
		t.Fatal("expected an error when the password is missing")
	}
	if !strings.Contains(err.Error(), "Password is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRenderRejectsUnknownTemplate(t *testing.T) {
	if _, err := Render("nope", nil); err == nil {
		t.Fatal("expected an error for an unknown template")
	}
}

func TestAutoinstallPasswordIsHashed(t *testing.T) {
	out, err := Render("ubuntu-autoinstall", map[string]string{"password": "Sup3rSecret!"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(out, "Sup3rSecret!") {
		t.Fatal("plain-text password leaked into the autoinstall file")
	}
	var hash string
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "password:") {
			hash = strings.Trim(strings.TrimSpace(strings.SplitN(line, ":", 2)[1]), `"`)
		}
	}
	if hash == "" {
		t.Fatalf("no password line found:\n%s", out)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte("Sup3rSecret!")); err != nil {
		t.Fatalf("password line is not a bcrypt hash of the input: %v", err)
	}
}

func TestAutoinstallPackageList(t *testing.T) {
	out, err := Render("ubuntu-autoinstall", map[string]string{
		"password": "Sup3rSecret!",
		"packages": "curl, htop\njq",
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{"    - curl", "    - htop", "    - jq"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing package entry %q\n%s", want, out)
		}
	}
}

func TestValidateRejectsBrokenFiles(t *testing.T) {
	cases := []struct {
		format, content string
	}{
		{"autoinstall", "autoinstall:\n  version: 1\n   bad indent: x\n"},
		{"autoinstall", "version: 1\n"},
		{"kickstart", "lang en_US\nautopart\n"},
		{"preseed", "d-i a string b\n"},
		{"autounattend", "<unattend><settings></unattend>"},
		{"nonsense", "whatever"},
	}
	for _, c := range cases {
		if err := Validate(c.format, c.content); err == nil {
			t.Errorf("Validate(%s) accepted invalid input: %q", c.format, c.content)
		}
	}
}

func TestOptionalDiskIsOmitted(t *testing.T) {
	out, err := Render("debian-preseed", map[string]string{"password": "pw12345678", "disk": ""})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(out, "partman-auto/disk") {
		t.Errorf("empty disk should not emit partman-auto/disk:\n%s", out)
	}

	out, err = Render("debian-preseed", map[string]string{"password": "pw12345678", "disk": "/dev/vda"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(out, "d-i partman-auto/disk string /dev/vda") {
		t.Errorf("explicit disk missing:\n%s", out)
	}
}
