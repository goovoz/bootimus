package autoinstall

// [bootimus-fork L1] fork-patch: autoinstall-generator
//
// 值进入答案文件之前必须先经过这里。text/template 不做任何转义（它本来就不是
// HTML/XML 模板），而四种格式的注入面完全不同：
//
//	autounattend  XML 文本节点 → 转义 & < > " '
//	autoinstall   YAML 双引号标量 → 转义 \ 与 "
//	preseed       「d-i 键 类型 值」一行一条，没有引号机制 → 只能靠单行 + 令牌字符约束
//	kickstart     同上，另外 --name=/--hostname= 都是裸值
//
// 这些不是洁癖：主机名里塞一个换行，就能在 preseed 里凭空多出一行 d-i 指令；
// 口令里有一个 &，整个 autounattend.xml 就不再是合法 XML。

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var (
	reHostname   = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)
	reUserName   = regexp.MustCompile(`^[a-z_][a-z0-9_-]*$`)
	reKeymap     = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	reLocale     = regexp.MustCompile(`^[A-Za-z0-9_@.-]+$`)
	reTimeZone   = regexp.MustCompile(`^[A-Za-z0-9_+/-]+$`)
	reDomain     = regexp.MustCompile(`^[A-Za-z0-9.-]*$`)
	reDisk       = regexp.MustCompile(`^/dev/[A-Za-z0-9/_.-]*$`)
	rePackage    = regexp.MustCompile(`^[A-Za-z0-9@^+._~:/-]+$`)
	reWinLocale  = regexp.MustCompile(`^[A-Za-z]{2,3}(-[A-Za-z0-9]+)*$`)
	reWinZone    = regexp.MustCompile(`^[A-Za-z0-9 _+-]+$`)
	reWinUser    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,19}$`)
	reProductKey = regexp.MustCompile(`^[A-Za-z0-9-]*$`)
	// 一个没声明 Placeholder 的字段里出现了「按客户端取值」的记号，说明前端漏配了
	reTokenRef = regexp.MustCompile(`^\{\{[A-Z][A-Z0-9_]*\}\}$`)
)

// varValuePatterns 是「这个字段允许出现哪些字符」。空值一律放行（可选字段）。
var varValuePatterns = map[string]*regexp.Regexp{
	"hostname":     reHostname,
	"username":     reUserName,
	"keymap":       reKeymap,
	"locale":       reLocale,
	"timezone":     reTimeZone,
	"domain":       reDomain,
	"disk":         reDisk,
	"win_locale":   reWinLocale,
	"win_timezone": reWinZone,
	"local_user":   reWinUser,
	"product_key":  reProductKey,
}

var sshKeyTypes = map[string]bool{
	"ssh-ed25519":                        true,
	"ssh-rsa":                            true,
	"ssh-dss":                            true,
	"ecdsa-sha2-nistp256":                true,
	"ecdsa-sha2-nistp384":                true,
	"ecdsa-sha2-nistp521":                true,
	"sk-ssh-ed25519@openssh.com":         true,
	"sk-ecdsa-sha2-nistp256@openssh.com": true,
}

func patternFor(format, name string) *regexp.Regexp {
	// Windows 的计算机名有自己的一套硬约束（<=15 字符、不能纯数字、空格要换掉），
	// 由 windowsComputerName 负责清洗，这里不能再用 Linux 主机名的规则卡它。
	if format == "autounattend" && name == "hostname" {
		return nil
	}
	return varValuePatterns[name]
}

func xmlEscape(s string) string {
	var buf bytes.Buffer
	if err := xml.EscapeText(&buf, []byte(s)); err != nil {
		return s
	}
	return buf.String()
}

func yamlEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s)
}

func checkCharacters(v Var, val string) error {
	for _, r := range val {
		switch {
		case r == '\n':
			if v.Type != "textarea" {
				return fmt.Errorf("%s must be a single line", v.Label)
			}
		case r == '\t':
			if v.Type != "textarea" {
				return fmt.Errorf("%s must not contain tabs", v.Label)
			}
		case r < 0x20 || r == 0x7f:
			return fmt.Errorf("%s contains a control character", v.Label)
		}
	}
	return nil
}

// sanitizeValue validates one form value and escapes it for the target format.
func sanitizeValue(format string, v Var, val string) (string, error) {
	if val == "" {
		return "", nil
	}
	if err := checkCharacters(v, val); err != nil {
		return "", err
	}

	if v.Type == "number" {
		if _, err := strconv.Atoi(val); err != nil || strings.HasPrefix(val, "-") {
			return "", fmt.Errorf("%s must be a whole number", v.Label)
		}
	}

	if re := patternFor(format, v.Name); re != nil && !re.MatchString(val) {
		if reTokenRef.MatchString(val) {
			return "", fmt.Errorf("%s: %s is a per-client token, but this field does not support it", v.Label, val)
		}
		return "", fmt.Errorf("%s: %q contains characters that would break the answer file", v.Label, val)
	}

	// Windows 的计算机名要先按 Windows 的规则清洗，再转义。顺序反了的话，
	// 15 字符的截断会落在 "&amp;" 中间，产出的 XML 直接非法。
	if format == "autounattend" && v.Name == "hostname" {
		val = windowsComputerName(val)
		if val == "*" {
			return val, nil
		}
	}

	switch v.Name {
	case "ssh_key":
		for _, line := range strings.Split(val, "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			if err := checkSingleSSHKey(line); err != nil {
				return "", err
			}
		}
	case "packages":
		for _, p := range splitPackages(val) {
			if !rePackage.MatchString(p) {
				return "", fmt.Errorf("%s: %q is not a valid package name", v.Label, p)
			}
		}
	}

	switch format {
	case "autounattend":
		return xmlEscape(val), nil
	case "autoinstall":
		if v.Type == "textarea" {
			// 多行字段会变成 YAML 列表项，不直接进标量
			return val, nil
		}
		return yamlEscape(val), nil
	}
	return val, nil
}

func checkSingleSSHKey(line string) error {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return errors.New("SSH public key must look like \"ssh-ed25519 AAAA... comment\"")
	}
	if !sshKeyTypes[fields[0]] {
		return fmt.Errorf("SSH key type %q is not recognised", fields[0])
	}
	for _, r := range line {
		if strings.ContainsRune("\"'\\`$", r) {
			return fmt.Errorf("SSH public key must not contain %q", r)
		}
		if r < 0x20 || r == 0x7f {
			return errors.New("SSH public key must not contain control characters")
		}
	}
	return nil
}

// splitSSHKeys turns the textarea into individual keys, one per line.
func splitSSHKeys(raw string) []string {
	var out []string
	for _, line := range strings.Split(raw, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}
