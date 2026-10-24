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
		"windows-autounattend": {"<ComputerName>*</ComputerName>", "<AcceptEula>true</AcceptEula>", "<LocalAccount"},
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
		// Windows 的答案文件是 image 级预置到 SMB 共享上的原文，服务端不做按客户端替换，
		// 因此这里不能声明占位符（否则 {{HOSTNAME}} 会原样变成计算机名）。
		if tpl.Format == "autounattend" {
			if hostnameVar.Placeholder != "" || hostnameVar.PerClient {
				t.Errorf("%s: Windows 答案文件不支持按客户端占位符，hostname 不应声明 Placeholder/PerClient", tpl.ID)
			}
			continue
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

// 只有声明了 Placeholder 的字段才支持「按客户端取值」。以前这种记号会被原样
// 写进答案文件（--name={{HOSTNAME}}），装上系统后才发现用户名错了；现在直接拒绝。
func TestPerClientTokenIsOnlyForDeclaredVars(t *testing.T) {
	_, err := Render("rhel-kickstart", map[string]string{
		"password": "Sup3rSecret!",
		"username": "{{HOSTNAME}}",
	})
	if err == nil {
		t.Fatal("undeclared per-client token should be rejected")
	}
	if !strings.Contains(err.Error(), "per-client") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// 口令里出现 & < > " ' 完全合法，但直接插进 XML 就会让整份 autounattend.xml 非法，
// 表现成「渲染失败：XML syntax error」——用户根本不知道是自己口令的问题。
func TestWindowsPasswordWithXMLSpecialsStaysWellFormed(t *testing.T) {
	const pw = `P&ss<w0>rd"'!`
	out, err := Render("windows-autounattend", map[string]string{"password": pw})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if err := Validate("autounattend", out); err != nil {
		t.Fatalf("produced invalid XML: %v", err)
	}
	if strings.Contains(out, "P&ss<w0>") {
		t.Errorf("口令没有被转义:\n%s", out)
	}
	if !strings.Contains(out, "P&amp;ss&lt;w0&gt;") {
		t.Errorf("缺少转义后的口令:\n%s", out)
	}
}

// /IMAGE/INDEX 与 DiskID 只能是整数；填了 "two" 以前会渲染成功，装到分区阶段才失败。
func TestNumericFieldsAreValidated(t *testing.T) {
	cases := map[string]map[string]string{
		"image_index 非数字": {"image_index": "two"},
		"disk_id 非数字":     {"disk_id": "first"},
		"image_index 负数":  {"image_index": "-1"},
		"image_index 小数":  {"image_index": "1.5"},
	}
	for name, extra := range cases {
		values := map[string]string{"password": "AdminPass123"}
		for k, v := range extra {
			values[k] = v
		}
		if _, err := Render("windows-autounattend", values); err == nil {
			t.Errorf("%s: 应该被拒绝", name)
		}
	}
}

// preseed 是「一行一条指令」，值里带换行就等于注入了一条新指令。
func TestSingleLineValuesAreEnforced(t *testing.T) {
	for _, bad := range []string{"node\nmalicious directive", "node\tx"} {
		if _, err := Render("debian-preseed", map[string]string{
			"password": "pw12345678",
			"hostname": bad,
		}); err == nil {
			t.Errorf("hostname %q 应被拒绝", bad)
		}
	}
}

// Linux 主机名/用户名不允许空格：kickstart 的 --name=my user 会直接解析失败。
func TestLinuxIdentifiersRejectSpaces(t *testing.T) {
	if _, err := Render("rhel-kickstart", map[string]string{
		"password": "pw12345678",
		"username": "my user",
	}); err == nil {
		t.Error("用户名带空格应被拒绝")
	}
	if _, err := Render("debian-preseed", map[string]string{
		"password": "pw12345678",
		"hostname": "my host",
	}); err == nil {
		t.Error("主机名带空格应被拒绝")
	}
}

// preseed 的 pkgsel 是空格分隔，kickstart 是一行一个包——不能把 textarea 原样塞进去。
func TestPackageListIsNormalisedPerFormat(t *testing.T) {
	out, err := Render("debian-preseed", map[string]string{
		"password": "pw12345678",
		"packages": "curl\nhtop\njq",
	})
	if err != nil {
		t.Fatalf("preseed render: %v", err)
	}
	if !strings.Contains(out, "d-i pkgsel/include string curl htop jq") {
		t.Errorf("preseed 的包列表应为空格分隔:\n%s", out)
	}

	out, err = Render("rhel-kickstart", map[string]string{
		"password": "pw12345678",
		"packages": "curl, htop\njq",
	})
	if err != nil {
		t.Fatalf("kickstart render: %v", err)
	}
	for _, want := range []string{"\ncurl\n", "\nhtop\n", "\njq\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("kickstart 的包应一行一个，缺少 %q\n%s", want, out)
		}
	}
}

func TestPackagesWithShellMetacharsRejected(t *testing.T) {
	if _, err := Render("debian-preseed", map[string]string{
		"password": "pw12345678",
		"packages": "curl; rm -rf /",
	}); err == nil {
		t.Error("包名里的 shell 元字符应被拒绝")
	}
}

// 多个公钥要展开成多条指令，而不是把带换行的字符串塞进一条命令。
func TestSSHKeysExpandPerLine(t *testing.T) {
	keys := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIalpha lab\nssh-rsa AAAAB3NzaC1yc2EAAAAIbeta ops@host"

	out, err := Render("debian-preseed", map[string]string{"password": "pw12345678", "ssh_key": keys})
	if err != nil {
		t.Fatalf("preseed render: %v", err)
	}
	if strings.Count(out, "/target/root/.ssh/authorized_keys &&") != 2 {
		t.Errorf("preseed 应有两条 echo，实际:\n%s", out)
	}
	if !strings.Contains(out, `echo "ssh-ed25519`) || !strings.Contains(out, `echo "ssh-rsa`) {
		t.Errorf("preseed 缺少公钥:\n%s", out)
	}

	out, err = Render("rhel-kickstart", map[string]string{"password": "pw12345678", "ssh_key": keys})
	if err != nil {
		t.Fatalf("kickstart render: %v", err)
	}
	if strings.Count(out, "sshkey --username=") != 2 {
		t.Errorf("kickstart 应有两条 sshkey，实际:\n%s", out)
	}

	out, err = Render("ubuntu-autoinstall", map[string]string{"password": "pw12345678", "ssh_key": keys})
	if err != nil {
		t.Fatalf("autoinstall render: %v", err)
	}
	if err := Validate("autoinstall", out); err != nil {
		t.Fatalf("autoinstall 产物不是合法 YAML: %v", err)
	}
	if strings.Count(out, ">> /root/.ssh/authorized_keys'") != 2 {
		t.Errorf("autoinstall 应有两条追加公钥的命令:\n%s", out)
	}
	if !strings.Contains(out, `echo "ssh-rsa`) {
		t.Errorf("autoinstall 缺少第二个公钥:\n%s", out)
	}
}

func TestBrokenSSHKeyRejected(t *testing.T) {
	for _, bad := range []string{
		"not-a-key",
		`ssh-ed25519 AAAA"'; rm -rf /`,
	} {
		if _, err := Render("ubuntu-autoinstall", map[string]string{
			"password": "pw12345678",
			"ssh_key":  bad,
		}); err == nil {
			t.Errorf("公钥 %q 应被拒绝", bad)
		}
	}
}

func TestYAMLEscape(t *testing.T) {
	got := yamlEscape(`a"b\c`)
	if got != `a\"b\\c` {
		t.Fatalf("yamlEscape = %q", got)
	}
}

// Windows Setup 只认与固件一致的分区表：BIOS 启动装到 GPT 盘上会直接报
// "Windows cannot be installed to this disk ... GPT partition style"。
// 模板必须两种方案都能出，且 InstallTo 分区号跟着走。
func TestWindowsFirmwareSelectsPartitionLayout(t *testing.T) {
	cases := []struct {
		firmware string
		want     []string
		notWant  []string
	}{
		{
			firmware: "uefi",
			want:     []string{"<Type>EFI</Type>", "<Type>MSR</Type>", "<Format>FAT32</Format>", "<PartitionID>3</PartitionID>"},
			notWant:  []string{"<Active>true</Active>"},
		},
		{
			firmware: "bios",
			want:     []string{"<Active>true</Active>", "<Size>500</Size>", "<PartitionID>2</PartitionID>"},
			notWant:  []string{"<Type>EFI</Type>", "<Type>MSR</Type>", "<Format>FAT32</Format>"},
		},
	}
	for _, c := range cases {
		out, err := Render("windows-autounattend", map[string]string{
			"password": "AdminPass123",
			"firmware": c.firmware,
		})
		if err != nil {
			t.Fatalf("%s: render: %v", c.firmware, err)
		}
		if err := Validate("autounattend", out); err != nil {
			t.Fatalf("%s: produced invalid XML: %v", c.firmware, err)
		}
		for _, want := range c.want {
			if !strings.Contains(out, want) {
				t.Errorf("%s: missing %q\n%s", c.firmware, want, out)
			}
		}
		for _, nw := range c.notWant {
			if strings.Contains(out, nw) {
				t.Errorf("%s: must not contain %q\n%s", c.firmware, nw, out)
			}
		}
	}
}

func TestWindowsFirmwareDefaultsToUEFI(t *testing.T) {
	out, err := Render("windows-autounattend", map[string]string{"password": "AdminPass123"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(out, "<Type>EFI</Type>") {
		t.Errorf("默认应为 UEFI/GPT:\n%s", out)
	}
}

func TestWindowsComputerNameIsSanitised(t *testing.T) {
	cases := map[string]string{
		"Lab PC 01":            "Lab-PC-01",
		"verylongmachine-name": "verylongmachine", // 截断到 15
		"12345":                "*",               // 纯数字 -> 交给 Windows 生成
		"  ":                   "*",
		"{{HOSTNAME}}":         "HOSTNAME", // 花括号被清掉后还能救回来
	}
	for in, want := range cases {
		out, err := Render("windows-autounattend", map[string]string{
			"password": "AdminPass123",
			"hostname": in,
		})
		if err != nil {
			t.Fatalf("%q: render: %v", in, err)
		}
		got := between(out, "<ComputerName>", "</ComputerName>")
		if got != want {
			t.Errorf("ComputerName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWindowsComputerNameKeepsStar(t *testing.T) {
	out, err := Render("windows-autounattend", map[string]string{"password": "AdminPass123"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if got := between(out, "<ComputerName>", "</ComputerName>"); got != "*" {
		t.Fatalf("默认 ComputerName 应为 *（Windows 自行生成），实际 %q", got)
	}
}

func between(s, open, close string) string {
	i := strings.Index(s, open)
	if i < 0 {
		return ""
	}
	i += len(open)
	j := strings.Index(s[i:], close)
	if j < 0 {
		return ""
	}
	return s[i : i+j]
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

// 镜像站点必须「实测能提供该发行版的仓库」才允许出现在下拉里。
// Debian/Ubuntu 十家全通过；Rocky/Alma 只有阿里云、华为云、腾讯云、南大四家。
func TestMirrorOptionsOnlyListVerifiedSites(t *testing.T) {
	mirrorOf := func(id string) map[string]Option {
		tpl, ok := TemplateByID(id)
		if !ok {
			t.Fatalf("template %q missing", id)
		}
		out := map[string]Option{}
		for _, v := range tpl.Vars {
			if v.Name == "mirror" {
				for _, o := range v.Options {
					out[o.Value] = o
				}
			}
		}
		if len(out) == 0 {
			t.Fatalf("%s: 没有 mirror 变量", id)
		}
		return out
	}

	for _, id := range []string{"debian-preseed", "ubuntu-autoinstall"} {
		got := mirrorOf(id)
		for _, want := range []string{"official", "aliyun", "huaweicloud", "tencent", "ustc", "tuna", "netease", "nju", "hust"} {
			if _, ok := got[want]; !ok {
				t.Errorf("%s: 缺少镜像选项 %s", id, want)
			}
		}
	}

	ks := mirrorOf("rhel-kickstart")
	for _, want := range []string{"official", "aliyun", "huaweicloud", "tencent", "nju"} {
		if _, ok := ks[want]; !ok {
			t.Errorf("rhel-kickstart: 缺少镜像选项 %s", want)
		}
	}
	for _, bad := range []string{"tuna", "netease", "ustc", "hust"} {
		if _, ok := ks[bad]; ok {
			t.Errorf("rhel-kickstart: %s 没有 Rocky/Alma 仓库，不应出现", bad)
		}
	}
}

func TestPreseedMirrorRendersHostAndPath(t *testing.T) {
	cases := []struct {
		mirror, family, wantHost, wantDir string
	}{
		{"official", "debian", "deb.debian.org", "debian"},
		{"official", "ubuntu", "archive.ubuntu.com", "ubuntu"},
		{"aliyun", "debian", "mirrors.aliyun.com", "debian"},
		{"aliyun", "ubuntu", "mirrors.aliyun.com", "ubuntu"},
		{"tuna", "ubuntu", "mirrors.tuna.tsinghua.edu.cn", "ubuntu"},
		{"hust", "debian", "mirrors.hust.edu.cn", "debian"},
	}
	for _, c := range cases {
		out, err := Render("debian-preseed", map[string]string{
			"password":      "pw12345678",
			"mirror":        c.mirror,
			"mirror_family": c.family,
		})
		if err != nil {
			t.Fatalf("%s/%s: render: %v", c.mirror, c.family, err)
		}
		if got := "d-i mirror/http/hostname string " + c.wantHost; !strings.Contains(out, got) {
			t.Errorf("%s/%s: 缺少 %q\n%s", c.mirror, c.family, got, out)
		}
		if got := "d-i mirror/http/directory string " + c.wantDir; !strings.Contains(out, got) {
			t.Errorf("%s/%s: 缺少 %q\n%s", c.mirror, c.family, got, out)
		}
	}
}

func TestAutoinstallMirrorEmitsAptPrimary(t *testing.T) {
	out, err := Render("ubuntu-autoinstall", map[string]string{
		"password": "pw12345678",
		"mirror":   "official",
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(out, "apt:") {
		t.Errorf("选 Official 时不应写 apt 段（交回 subiquity 默认）:\n%s", out)
	}

	out, err = Render("ubuntu-autoinstall", map[string]string{
		"password": "pw12345678",
		"mirror":   "aliyun",
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{"  apt:", "    primary:", "        uri: http://mirrors.aliyun.com/ubuntu"} {
		if !strings.Contains(out, want) {
			t.Errorf("缺少 %q\n%s", want, out)
		}
	}
	if err := Validate("autoinstall", out); err != nil {
		t.Errorf("带 apt 段的产物不是合法 YAML: %v\n%s", err, out)
	}
}

func TestKickstartMirrorRewritesRepo(t *testing.T) {
	out, err := Render("rhel-kickstart", map[string]string{
		"password":    "pw12345678",
		"mirror":      "official",
		"repo_family": "rocky",
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(out, "yum.repos.d") {
		t.Errorf("选 Official 时不应改 dnf 源:\n%s", out)
	}

	out, err = Render("rhel-kickstart", map[string]string{
		"password":    "pw12345678",
		"mirror":      "aliyun",
		"repo_family": "rocky",
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{
		"baseurl=http://mirrors.aliyun.com/rockylinux",
		"/etc/yum.repos.d/rocky*.repo",
		"$contentdir",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rocky: 缺少 %q\n%s", want, out)
		}
	}

	out, err = Render("rhel-kickstart", map[string]string{
		"password":    "pw12345678",
		"mirror":      "tencent",
		"repo_family": "almalinux",
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{
		"baseurl=http://mirrors.cloud.tencent.com/almalinux",
		"/etc/yum.repos.d/almalinux*.repo",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("almalinux: 缺少 %q\n%s", want, out)
		}
	}
	if strings.Contains(out, "rocky*.repo") {
		t.Errorf("almalinux 分支不应碰 rocky*.repo:\n%s", out)
	}
}
