package autoinstall

// [bootimus-fork L1] fork-patch: autoinstall-generator 的回归测试。
// 与实现同生共死：上游若自带模板生成能力，本文件连同 generate.go 一并删除。

import (
	"strings"
	"testing"
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

// 文件名决定服务端 Content-Type 与类型判定（.cfg=preseed / .ks=kickstart），
// 官方文档明确按扩展名区分，这里卡住默认值。
func TestDefaultFilenamesMatchFormat(t *testing.T) {
	want := map[string]string{
		"preseed":      ".cfg",
		"kickstart":    ".ks",
		"autoinstall":  ".yaml",
		"autounattend": ".xml",
	}
	for _, tpl := range Templates() {
		suffix, ok := want[tpl.Format]
		if !ok {
			t.Errorf("%s: unknown format %q", tpl.ID, tpl.Format)
			continue
		}
		if !strings.HasSuffix(tpl.Filename, suffix) {
			t.Errorf("%s: filename %q should end in %s", tpl.ID, tpl.Filename, suffix)
		}
	}
}

func TestEveryTemplateRenders(t *testing.T) {
	markers := map[string][]string{
		"debian-preseed":       {"d-i passwd/username string admin", "d-i netcfg/get_hostname string bootimus-node"},
		"rhel-kickstart":       {"user --name=admin", "%packages", "autopart --type=lvm"},
		"ubuntu-autoinstall":   {"autoinstall:", `hostname: "bootimus-node"`, "layout:"},
		"windows-autounattend": {"<ComputerName>BOOTIMUS-PC</ComputerName>", "<AcceptEula>true</AcceptEula>", "<LocalAccount"},
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
		if tpl.Format != "autounattend" && strings.Contains(out, "Sup3rSecret!") {
			t.Errorf("%s: 明文口令泄漏到文件里\n%s", tpl.ID, out)
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
		"rootpw --iscrypted $6$",
		"autopart --type=plain",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q\n%s", want, out)
		}
	}
	if strings.Contains(out, "hunter2hunter2") || strings.Contains(out, "rootpw123456") {
		t.Errorf("plain-text password leaked:\n%s", out)
	}
}

func TestPreseedUsesCryptedPasswords(t *testing.T) {
	out, err := Render("debian-preseed", map[string]string{"password": "only-one-pass"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(out, "d-i passwd/user-password-crypted password $6$") {
		t.Errorf("preseed 应写 crypted 口令:\n%s", out)
	}
	// root 口令留空时不写 root 口令行（root 登录默认关闭）
	if strings.Contains(out, "passwd/root-password") {
		t.Errorf("root 口令留空时不应出现 root-password 行:\n%s", out)
	}
	if strings.Contains(out, "d-i passwd/root-login boolean true") {
		t.Errorf("root login 默认应关闭:\n%s", out)
	}
}

func TestKickstartLocksRootWhenNoRootPassword(t *testing.T) {
	out, err := Render("rhel-kickstart", map[string]string{"password": "pw12345678"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(out, "rootpw --lock") {
		t.Errorf("未给 root 口令时应锁 root:\n%s", out)
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
	if !strings.HasPrefix(hash, "$6$") {
		t.Fatalf("Ubuntu autoinstall 需要 sha512-crypt（$6$）口令，实际 %q", hash)
	}
}

func TestWindowsCreatesLocalAccount(t *testing.T) {
	out, err := Render("windows-autounattend", map[string]string{
		"password":       "AdminPass123",
		"local_user":     "labuser",
		"local_password": "LabPass456",
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{
		`<LocalAccount wcm:action="add"`,
		"<Name>labuser</Name>",
		"<Group>Administrators</Group>",
		"<Value>LabPass456</Value>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q\n%s", want, out)
		}
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

func TestPerClientHostnameKeepsToken(t *testing.T) {
	for _, tpl := range Templates() {
		var hostnameVar Var
		for _, v := range tpl.Vars {
			if v.Name == "hostname" {
				hostnameVar = v
			}
		}
		if hostnameVar.Placeholder == "" {
			t.Errorf("%s: hostname 没有声明占位符", tpl.ID)
			continue
		}

		values := fillValues(t, tpl)
		values["hostname"] = hostnameVar.Placeholder
		out, err := Render(tpl.ID, values)
		if err != nil {
			t.Fatalf("%s: 按客户端渲染失败: %v", tpl.ID, err)
		}
		if !strings.Contains(out, hostnameVar.Placeholder) {
			t.Errorf("%s: 输出里没有保留 %s\n%s", tpl.ID, hostnameVar.Placeholder, out)
		}
		if strings.Contains(out, "\x00") {
			t.Errorf("%s: 哨兵字符泄漏到输出里\n%s", tpl.ID, out)
		}
		if err := Validate(tpl.Format, out); err != nil {
			t.Errorf("%s: 带占位符的产物没通过校验: %v\n%s", tpl.ID, err, out)
		}
	}
}

func TestPerClientTokenIsOnlyForDeclaredVars(t *testing.T) {
	out, err := Render("rhel-kickstart", map[string]string{
		"password": "Sup3rSecret!",
		"username": "{{HOSTNAME}}",
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(out, "--name={{HOSTNAME}}") {
		t.Errorf("未声明的字段不应被当作占位符处理:\n%s", out)
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
