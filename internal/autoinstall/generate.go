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
	"golang.org/x/crypto/bcrypt"
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

func passwordVar(name, label string, required bool) Var {
	return Var{Name: name, Label: label, Type: "password", Help: "Stored in the generated file as plain text — keep the file on a trusted network.", Required: required}
}

func selectVar(name, label, def string, opts ...Option) Var {
	return Var{Name: name, Label: label, Type: "select", Default: def, Options: opts}
}

func opt(value, label string) Option { return Option{Value: value, Label: label} }

func commonVars() []Var {
	return []Var{
		{
			Name: "hostname", Label: "Host name", Type: "text", Default: "bootimus-node",
			Help: "Set per client to give each machine its own name.", Required: true, PerClient: true,
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
		Name:     "Debian / Ubuntu (preseed)",
		Format:   "preseed",
		Distro:   "debian",
		Filename: "preseed.cfg",
		Summary:  "Legacy debian-installer preseed for Debian and Ubuntu 20.04 and older.",
		body:     "templates/preseed.tmpl",
		Vars: append(commonVars(),
			text("domain", "Domain", "local", "", false),
			text("mirror_host", "Mirror host", "deb.debian.org", "Point this at a local or regional mirror.", false),
			text("mirror_dir", "Mirror directory", "debian", "e.g. debian, ubuntu", false),
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
		Filename: "ks.cfg",
		Summary:  "Anaconda kickstart for the Red Hat family, RHEL 8/9/10 and clones.",
		body:     "templates/kickstart.tmpl",
		Vars: append(commonVars(),
			passwordVar("root_password", "Root password (optional)", false),
			selectVar("partition", "Partitioning", "lvm",
				opt("lvm", "LVM"), opt("btrfs", "Btrfs"), opt("plain", "Plain partitions")),
		),
	},
	{
		ID:       "ubuntu-autoinstall",
		Name:     "Ubuntu Server 20.04+ (autoinstall)",
		Format:   "autoinstall",
		Distro:   "ubuntu",
		Filename: "autoinstall.yaml",
		Summary:  "Subiquity autoinstall for Ubuntu Server 20.04 and newer; the password is hashed (bcrypt) before it is written.",
		body:     "templates/ubuntu-autoinstall.tmpl",
		Vars: append(commonVars(),
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
		Summary:  "UEFI single-disk autounattend.xml. Windows 11 still needs TPM 2.0 and Secure Boot unless the image is bypassed.",
		body:     "templates/autounattend.tmpl",
		Vars: []Var{
			text("hostname", "Computer name", "BOOTIMUS-PC", "15 characters max, no special characters.", true),
			passwordVar("password", "Administrator password", true),
			text("win_locale", "Windows locale", "zh-CN", "e.g. zh-CN, en-US", true),
			text("win_timezone", "Windows time zone", "China Standard Time", "Windows zone name, e.g. China Standard Time, UTC", true),
			text("image_index", "Install image index", "1", "Index inside install.wim / install.esd (/IMAGE/INDEX).", true),
			text("disk_id", "Disk number", "0", "", true),
			text("product_key", "Product key (optional)", "", "Leave empty to skip the key and activate later.", false),
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
	for _, v := range tpl.Vars {
		val := strings.TrimSpace(values[v.Name])
		if val == "" {
			val = v.Default
		}
		if v.Required && val == "" {
			return "", fmt.Errorf("%s is required", v.Label)
		}
		data[v.Name] = val
	}

	// preseed/kickstart：root 密码留空时跟普通用户一致，免得生成一个空口令的 root
	if _, ok := data["root_password"]; ok {
		if s, _ := data["root_password"].(string); s == "" {
			data["root_password"] = data["password"]
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

	if tpl.Format == "autoinstall" {
		pw, _ := data["password"].(string)
		hash, hashErr := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
		if hashErr != nil {
			return "", fmt.Errorf("hash password: %w", hashErr)
		}
		data["hashed_password"] = string(hash)
	}
	if pkgs, ok := data["packages"].(string); ok && tpl.Format == "autoinstall" {
		data["package_list"] = splitPackages(pkgs)
	}

	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("render %q: %w", id, err)
	}

	out := strings.TrimRight(buf.String(), "\n") + "\n"
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
