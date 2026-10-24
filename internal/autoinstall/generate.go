package autoinstall

// [bootimus-fork L1] fork-patch: autoinstall-generator
//
// 上游只提供「自动安装文件」的存取（List/Read/Write/Delete + 按客户端下发），
// 没有任何生成能力：用户要自己写 autounattend.xml / kickstart / preseed / autoinstall。
// 这里补一层「模板 + 变量 → 渲染 → 校验」，渲染结果仍然走 Library.Write，
// 因而与现有的「附加到镜像 / 按客户端覆盖」逻辑完全兼容。

import (
	"bytes"
	"embed"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"text/template"

	"go.yaml.in/yaml/v3"
)

//go:embed templates/*.tmpl
var templateFiles embed.FS

// Option is one entry of a select field.
type Option struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// Var describes one field of a template's form.
type Var struct {
	Name      string   `json:"name"`
	Label     string   `json:"label"`
	Type      string   `json:"type"` // text | textarea | password | number | bool | select
	Default   string   `json:"default,omitempty"`
	Help      string   `json:"help,omitempty"`
	Options   []Option `json:"options,omitempty"`
	Required  bool     `json:"required,omitempty"`
	PerClient bool     `json:"per_client,omitempty"`
	// Placeholder is the token bootimus substitutes when the file is served for
	// a specific client ({{HOSTNAME}}, {{MAC}}, ...). When the form value equals
	// it, the rendered file keeps the token instead of a baked-in literal.
	Placeholder string `json:"placeholder,omitempty"`
}

// Template is one built-in answer-file template.
type Template struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Format   string `json:"format"` // preseed | kickstart | autoinstall | autounattend
	Distro   string `json:"distro"` // suggested distro profile id
	Filename string `json:"filename"`
	Summary  string `json:"summary"`
	Vars     []Var  `json:"vars"`

	body string
}

// Formats lists the answer-file formats the generator understands.
func Formats() []string { return []string{"preseed", "kickstart", "autoinstall", "autounattend"} }

func text(name, label, def, help string, required bool) Var {
	return Var{Name: name, Label: label, Type: "text", Default: def, Help: help, Required: required}
}

func textarea(name, label, def, help string) Var {
	return Var{Name: name, Label: label, Type: "textarea", Default: def, Help: help}
}

// numberVar is a text field that only accepts a whole number.
func numberVar(name, label, def, help string) Var {
	return Var{Name: name, Label: label, Type: "number", Default: def, Help: help, Required: true}
}

func passwordVar(name, label string, required bool) Var {
	return Var{Name: name, Label: label, Type: "password", Help: "Stored in the generated file as plain text — keep the file on a trusted network.", Required: required}
}

func selectVar(name, label, def string, opts ...Option) Var {
	return Var{Name: name, Label: label, Type: "select", Default: def, Options: opts}
}

func opt(value, label string) Option { return Option{Value: value, Label: label} }

// selectVarHelp is selectVar with an explanatory line under the field.
func selectVarHelp(name, label, def, help string, opts ...Option) Var {
	v := selectVar(name, label, def, opts...)
	v.Help = help
	return v
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

// mirrorSite is one package-mirror location. Paths maps a distro family to the
// directory that actually serves its repository there; a missing entry means the
// mirror does not carry that family, so mirrorOptions() refuses to offer it for a
// template that needs it. Every path below was verified from the deployment
// network by fetching the repository index itself (apt Release / dnf repomd.xml),
// never by assuming that a directory exists: a mirror can answer 200 with an HTML
// challenge page, or carry Debian but not Rocky.
type mirrorSite struct {
	Value string
	Label string
	Host  string
	Paths map[string]string
}

var mirrorSites = []mirrorSite{
	{Value: "official", Label: "Official (installer default)"},
	{
		Value: "aliyun", Label: "Aliyun (China)", Host: "mirrors.aliyun.com",
		Paths: map[string]string{"debian": "debian", "ubuntu": "ubuntu", "rocky": "rockylinux", "almalinux": "almalinux"},
	},
	{
		Value: "huaweicloud", Label: "Huawei Cloud (China)", Host: "mirrors.huaweicloud.com",
		Paths: map[string]string{"debian": "debian", "ubuntu": "ubuntu", "rocky": "rockylinux", "almalinux": "almalinux"},
	},
	{
		Value: "tencent", Label: "Tencent Cloud (China)", Host: "mirrors.cloud.tencent.com",
		Paths: map[string]string{"debian": "debian", "ubuntu": "ubuntu", "rocky": "rocky", "almalinux": "almalinux"},
	},
	{
		Value: "ustc", Label: "USTC (China)", Host: "mirrors.ustc.edu.cn",
		Paths: map[string]string{"debian": "debian", "ubuntu": "ubuntu", "rocky": "rocky"},
	},
	{
		Value: "tuna", Label: "TUNA / Tsinghua (China)", Host: "mirrors.tuna.tsinghua.edu.cn",
		Paths: map[string]string{"debian": "debian", "ubuntu": "ubuntu"},
	},
	{
		Value: "netease", Label: "NetEase (China)", Host: "mirrors.163.com",
		Paths: map[string]string{"debian": "debian", "ubuntu": "ubuntu"},
	},
	{
		Value: "nju", Label: "NJU (China)", Host: "mirrors.nju.edu.cn",
		Paths: map[string]string{"debian": "debian", "ubuntu": "ubuntu", "rocky": "rocky", "almalinux": "almalinux"},
	},
	{
		Value: "hust", Label: "HUST (China)", Host: "mirrors.hust.edu.cn",
		Paths: map[string]string{"debian": "debian", "ubuntu": "ubuntu", "rocky": "rocky"},
	},
}

// mirrorOptions lists the sites that carry every requested family, in display order.
func mirrorOptions(families ...string) []Option {
	out := make([]Option, 0, len(mirrorSites))
	for _, s := range mirrorSites {
		if s.Paths == nil {
			out = append(out, opt(s.Value, s.Label))
			continue
		}
		ok := true
		for _, f := range families {
			if s.Paths[f] == "" {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, opt(s.Value, s.Label))
		}
	}
	return out
}

func mirrorSiteByValue(value string) mirrorSite {
	for _, s := range mirrorSites {
		if s.Value == value {
			return s
		}
	}
	return mirrorSite{}
}

func commonVars() []Var {
	return []Var{
		{
			Name: "hostname", Label: "Host name", Type: "text", Default: "bootimus-node",
			Help:     "Tick the per-client box to keep the {{HOSTNAME}} token and let bootimus fill in each client's name.",
			Required: true, PerClient: true, Placeholder: "{{HOSTNAME}}",
		},
		text("username", "User name", "admin", "", true),
		passwordVar("password", "Password", true),
		text("timezone", "Time zone", "Asia/Shanghai", "", true),
		text("locale", "Locale", "en_US.UTF-8", "Preseed and kickstart use the glibc form (en_US.UTF-8); Ubuntu accepts the same value.", true),
		text("keymap", "Keyboard layout", "us", "", true),
		textarea("ssh_key", "SSH public key (optional)", "", "Pasted into /root/.ssh/authorized_keys."),
		textarea("packages", "Extra packages (one per line)", "", ""),
	}
}

var templates = []Template{
	{
		ID:       "debian-preseed",
		Name:     "Debian / Ubuntu up to 20.04 (preseed)",
		Format:   "preseed",
		Distro:   "debian",
		Filename: "preseed.cfg",
		Summary:  "Legacy debian-installer preseed. Debian uses it, and so did Ubuntu up to 20.04. Ubuntu Server 20.04 and newer installs with autoinstall (cloud-init) instead — use that template for current Ubuntu.",
		body:     "templates/preseed.tmpl",
		Vars: append(commonVars(),
			text("domain", "Domain", "local", "", false),
			selectVarHelp("mirror", "Repository mirror", "official", "Every site listed here was verified from the deployment network to serve the full Debian and Ubuntu package index. Official keeps deb.debian.org / archive.ubuntu.com.", mirrorOptions("debian", "ubuntu")...),
			selectVarHelp("mirror_family", "Repository path", "debian", "Debian and Ubuntu keep separate trees on every mirror. Ubuntu 20.04 and newer should use the autoinstall template instead of preseed.", opt("debian", "Debian (debian)"), opt("ubuntu", "Ubuntu (ubuntu)")),
			selectVar("root_login", "Allow root login", "false",
				opt("false", "Disabled"), opt("true", "Enabled")),
			passwordVar("root_password", "Root password (optional)", false),
			selectVar("recipe", "Partition recipe", "atomic",
				opt("atomic", "All files in one partition"),
				opt("home", "Separate /home"),
				opt("multi", "Separate /home, /var and /tmp")),
			selectVar("upgrade", "Upgrade during install", "none",
				opt("none", "No upgrade"), opt("safe", "Safe-upgrade"), opt("full", "Full-upgrade")),
			selectVar("tasks", "Task selection", "standard, ssh-server",
				opt("standard", "Standard system"), opt("standard, ssh-server", "Standard + SSH server"), opt("none", "None")),
			text("disk", "Target disk (optional)", "", "Empty = let the installer pick the only disk. e.g. /dev/sda, /dev/vda, /dev/nvme0n1", false),
		),
	},
	{
		ID:       "rhel-kickstart",
		Name:     "RHEL / Rocky / AlmaLinux / Fedora (kickstart)",
		Format:   "kickstart",
		Distro:   "rocky",
		Filename: "kickstart.ks",
		Summary:  "Anaconda kickstart for the Red Hat family, RHEL 8/9/10 and clones. The installer reads packages from the ISO tree bootimus serves, so a mirror only repoints dnf after install.",
		body:     "templates/kickstart.tmpl",
		Vars: append(commonVars(),
			passwordVar("root_password", "Root password (optional)", false),
			selectVarHelp("mirror", "Repository mirror", "official", "Only repoints dnf after install — the installer itself still installs from the ISO tree served by bootimus, which is faster than any mirror. Sites without the Rocky/Alma repositories, and RHEL or Fedora (subscription/metalink), are not listed: leave this on Official for those.", mirrorOptions("rocky", "almalinux")...),
			selectVarHelp("repo_family", "Repository family", "rocky", "Which repository files the mirror rewrite applies to; pick the family you are actually installing.", opt("rocky", "Rocky Linux"), opt("almalinux", "AlmaLinux")),
			selectVar("partition", "Partitioning", "lvm",
				opt("lvm", "LVM"), opt("btrfs", "Btrfs"), opt("plain", "Plain partitions")),
		),
	},
	{
		ID:       "ubuntu-autoinstall",
		Name:     "Ubuntu Server 20.04+ (autoinstall / cloud-init)",
		Format:   "autoinstall",
		Distro:   "ubuntu",
		Filename: "autoinstall.yaml",
		Summary:  "Subiquity autoinstall — the cloud-init based path that Ubuntu Server 20.04 and newer uses (this is what the docs mean by \"Ubuntu uses cloud-init\"). The password is hashed on the server before it is written.",
		body:     "templates/ubuntu-autoinstall.tmpl",
		Vars: append(commonVars(),
			selectVarHelp("mirror", "Repository mirror", "official", "Subiquity pulls packages from the archive while installing. Official follows the geoip default, usually archive.ubuntu.com — slow from mainland China.", mirrorOptions("ubuntu")...),
			selectVar("storage_layout", "Storage layout", "direct",
				opt("direct", "Direct (single partition)"), opt("lvm", "LVM"), opt("zfs", "ZFS")),
		),
	},
	{
		ID:       "windows-autounattend",
		Name:     "Windows 10 / 11 (autounattend.xml)",
		Format:   "autounattend",
		Distro:   "windows",
		Filename: "autounattend.xml",
		Summary:  "Single-disk autounattend.xml. Set the boot mode to match the client's PXE firmware: UEFI builds GPT, BIOS builds MBR, and a mismatch aborts Setup with \"Windows cannot be installed to this disk: the selected disk is of the GPT partition style\". Windows 11 still needs TPM 2.0 and Secure Boot unless the image is bypassed.",
		body:     "templates/autounattend.tmpl",
		Vars: []Var{
			{
				Name: "firmware", Label: "Boot mode (firmware)", Type: "select", Default: "uefi",
				Options: []Option{
					{Value: "uefi", Label: "UEFI (GPT)"},
					{Value: "bios", Label: "BIOS / legacy (MBR)"},
				},
				Help: "Must match how this client PXE-boots. The bootimus log shows arch=0 / undionly.kpxe for BIOS and arch=7 / ipxe.efi for UEFI.",
			},
			{
				Name: "hostname", Label: "Computer name", Type: "text", Default: "*",
				Help: "* = Windows generates DESKTOP-XXXXXXX for each machine. A fixed value (15 chars max, no spaces) is baked into this one file, which every client booting the image shares.",
			},
			passwordVar("password", "Administrator password", true),
			text("local_user", "Local account name", "user", "Created as a member of the Administrators group.", true),
			passwordVar("local_password", "Local account password", false),
			text("win_locale", "Windows locale", "zh-CN", "e.g. zh-CN, en-US", true),
			text("win_timezone", "Windows time zone", "China Standard Time", "Windows zone name, e.g. China Standard Time, UTC", true),
			numberVar("image_index", "Install image index", "1", "Index inside install.wim / install.esd (/IMAGE/INDEX)."),
			numberVar("disk_id", "Disk number", "0", ""), text("product_key", "Product key (optional)", "", "Leave empty to skip the key and activate later.", false),
		},
	},
}

func resourceName(id string) string {
	for _, t := range templates {
		if t.ID == id {
			return t.body
		}
	}
	return ""
}

// Templates returns a copy of the built-in template catalogue (without bodies).
func Templates() []Template {
	out := make([]Template, len(templates))
	copy(out, templates)
	return out
}

// TemplateByID looks one template up.
func TemplateByID(id string) (Template, bool) {
	for _, t := range templates {
		if t.ID == id {
			return t, true
		}
	}
	return Template{}, false
}

// Render fills in a template and validates the result.
func Render(id string, values map[string]string) (string, error) {
	tpl, ok := TemplateByID(id)
	if !ok {
		return "", fmt.Errorf("unknown template %q", id)
	}

	data := make(map[string]any, len(tpl.Vars)+4)
	type tokenSwap struct{ sentinel, token string }
	var swaps []tokenSwap
	for _, v := range tpl.Vars {
		val := strings.TrimSpace(values[v.Name])
		if val == "" {
			val = v.Default
		}
		if v.Required && val == "" {
			return "", fmt.Errorf("%s is required", v.Label)
		}
		// 勾了「按客户端取值」时保留 token：模板里 {{HOSTNAME}} 这样的字面量不能直接写进
		// text/template（会被当成函数调用），所以先塞一个哨兵，执行完再换回 token。
		if v.Placeholder != "" && strings.EqualFold(val, v.Placeholder) {
			sentinel := "\x00pc:" + v.Name + "\x00"
			swaps = append(swaps, tokenSwap{sentinel, v.Placeholder})
			data[v.Name] = sentinel
			continue
		}
		// 转义与校验必须在这里做：模板执行之后就无法区分「值」和「语法」了。
		clean, err := sanitizeValue(tpl.Format, v, val)
		if err != nil {
			return "", err
		}
		data[v.Name] = clean
	}

	// 镜像站点：模板里只暴露一个站点下拉和一个 family 选择，host / 目录 / base URL
	// 全在这里换算，模板侧不做映射。选 Official 时不写任何 base，交回安装器默认。
	data["mirror_host"] = ""
	data["mirror_dir"] = ""
	data["mirror_base"] = ""
	data["repo_base"] = ""
	switch tpl.Format {
	case "preseed":
		family := asString(data["mirror_family"])
		if family == "" {
			family = "debian"
		}
		host, dir := "deb.debian.org", "debian"
		if family == "ubuntu" {
			host, dir = "archive.ubuntu.com", "ubuntu"
		}
		if site := mirrorSiteByValue(asString(data["mirror"])); site.Paths != nil {
			if p := site.Paths[family]; p != "" {
				host, dir = site.Host, p
			}
		}
		data["mirror_host"], data["mirror_dir"] = host, dir
	case "autoinstall":
		if site := mirrorSiteByValue(asString(data["mirror"])); site.Paths != nil {
			if p := site.Paths["ubuntu"]; p != "" {
				data["mirror_base"] = "http://" + site.Host + "/" + p
			}
		}
	case "kickstart":
		if site := mirrorSiteByValue(asString(data["mirror"])); site.Paths != nil {
			if p := site.Paths[asString(data["repo_family"])]; p != "" {
				data["repo_base"] = "http://" + site.Host + "/" + p
			}
		}
	}

	raw, err := templateFiles.ReadFile(tpl.body)
	if err != nil {
		return "", fmt.Errorf("template %q unavailable: %w", id, err)
	}
	t, err := template.New(id).Option("missingkey=error").Parse(string(raw))
	if err != nil {
		return "", fmt.Errorf("template %q is broken: %w", id, err)
	}

	// 安装器要的是 crypt(3) 哈希（$6$）：preseed 用 *-crypted，kickstart 用 --iscrypted，
	// Ubuntu autoinstall 用 identity.password，三处共用一份实现。
	if pw, ok := data["password"].(string); ok && pw != "" {
		hash := HashPassword(pw)
		if hash == "" {
			return "", fmt.Errorf("cannot hash the password")
		}
		data["user_hash"] = hash
		data["hashed_password"] = hash
	}
	// root 口令留空 -> 空字符串，模板据此走 --lock / 不写 root 行；
	// 始终设置该键，避免 missingkey=error 在 {{if .root_hash}} 上报错。
	rp, _ := data["root_password"].(string)
	data["root_hash"] = HashPassword(rp)

	// Windows 本地账户口令留空时沿用管理员口令，少填一次
	if lp, ok := data["local_password"].(string); ok && lp == "" {
		data["local_password"] = data["password"]
	}
	// 包列表按格式落地：preseed 的 pkgsel 是空格分隔、kickstart 一行一个包、
	// autoinstall 是 YAML 列表。以前三种都把 textarea 原样塞进去，换行会把
	// preseed 的一行指令截成两行。
	pkgs := asString(data["packages"])
	switch tpl.Format {
	case "preseed":
		data["packages"] = strings.Join(splitPackages(pkgs), " ")
	case "kickstart":
		data["packages"] = strings.Join(splitPackages(pkgs), "\n")
	case "autoinstall":
		data["package_list"] = splitPackages(pkgs)
	}
	// 公钥可能多行：按格式展开成多条指令 / 多行 echo，而不是把一个带换行的
	// 字符串塞进单条命令里。
	data["ssh_key_list"] = splitSSHKeys(asString(data["ssh_key"]))

	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("render %q: %w", id, err)
	}

	out := buf.String()
	for _, s := range swaps {
		out = strings.ReplaceAll(out, s.sentinel, s.token)
	}

	out = strings.TrimRight(out, "\n") + "\n"
	if err := Validate(tpl.Format, out); err != nil {
		return "", err
	}
	return out, nil
}

var packageSplit = regexp.MustCompile(`[\s,]+`)

func splitPackages(raw string) []string {
	var out []string
	for _, p := range packageSplit.Split(strings.TrimSpace(raw), -1) {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Validate runs a format-specific sanity check on a finished answer file.
func Validate(format, content string) error {
	switch format {
	case "autoinstall":
		var doc map[string]any
		if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
			return fmt.Errorf("autoinstall is not valid YAML: %w", err)
		}
		if _, ok := doc["autoinstall"]; !ok {
			return errors.New("autoinstall is missing the top-level autoinstall: key")
		}
	case "kickstart":
		for _, needle := range []string{"%packages", "%end", "rootpw"} {
			if !strings.Contains(content, needle) {
				return fmt.Errorf("kickstart is missing %q", needle)
			}
		}
	case "preseed":
		if strings.Count(content, "d-i ") < 5 {
			return errors.New("preseed does not look like a debian-installer file")
		}
	case "autounattend":
		dec := xml.NewDecoder(strings.NewReader(content))
		for {
			if _, err := dec.Token(); err != nil {
				if errors.Is(err, io.EOF) {
					break
				}
				return fmt.Errorf("autounattend.xml is malformed: %w", err)
			}
		}
	default:
		return fmt.Errorf("unsupported format %q", format)
	}
	return nil
}

// windowsComputerName coerces a value into something Windows Setup accepts:
// 15 characters or fewer, none of \ / : * ? " < > | or spaces, not all digits.
// Values that cannot be salvaged become "*" (let Windows generate the name).
func windowsComputerName(s string) string {
	s = strings.TrimSpace(s)
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '\\', '/', ':', '*', '?', '"', '<', '>', '|', ' ', '\t', '.', ',', ';', '{', '}', '(', ')':
			b.WriteRune('-')
		default:
			b.WriteRune(r)
		}
	}
	out := strings.Trim(b.String(), "-")
	if r := []rune(out); len(r) > 15 {
		out = string(r[:15])
	}
	out = strings.TrimRight(out, "-")

	allDigits := out != ""
	for _, r := range out {
		if r < '0' || r > '9' {
			allDigits = false
			break
		}
	}
	if out == "" || allDigits {
		return "*"
	}
	return out
}
