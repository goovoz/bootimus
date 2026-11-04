package admin

import (
	"strings"
	"testing"
)

func TestBuildStartnetScriptUnattendLookup(t *testing.T) {
	cases := []struct {
		name        string
		autoInstall bool
		wantShare   bool
	}{
		{"no image-level file", false, false},
		{"image-level file staged", true, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			script := buildStartnetScript("10.0.0.1", "win11", 445, 8080, "Win11.iso", c.autoInstall)
			if script == "" {
				t.Fatal("expected a rendered startnet.cmd")
			}
			injected := `if exist X:\Windows\System32\AutoUnattend.xml set UNATTEND=X:\Windows\System32\AutoUnattend.xml`
			fallback := `if not defined UNATTEND if exist Z:\AutoUnattend.xml (`
			apply := `if defined UNATTEND set SETUP_ARGS=!SETUP_ARGS! /unattend:!UNATTEND!`

			if !strings.Contains(script, injected) {
				t.Errorf("expected the wimboot-injected AutoUnattend.xml check:\n%s", script)
			}
			if got := strings.Contains(script, fallback); got != c.wantShare {
				t.Errorf("SMB fallback present=%v, want %v:\n%s", got, c.wantShare, script)
			}
			if !strings.Contains(script, apply) {
				t.Errorf("expected /unattend to be applied from UNATTEND:\n%s", script)
			}
			if strings.Index(script, injected) > strings.Index(script, apply) {
				t.Errorf("injected check must run before /unattend is applied:\n%s", script)
			}
			if c.wantShare && strings.Index(script, injected) > strings.Index(script, fallback) {
				t.Errorf("wimboot-injected file must take precedence over the share copy:\n%s", script)
			}
			if !strings.Contains(script, `\\10.0.0.1\win11`) {
				t.Errorf("expected the share path in the script:\n%s", script)
			}
		})
	}
}

// WinPE 拿不到网络时最需要的是「有没有网卡」这个事实：virtio 网卡在微软原版
// boot.wim 里没有驱动，ipconfig 全空就是这个原因。脚本自动打一遍，省得让用户猜。
func TestBuildStartnetScriptShowsAdaptersOnNetfail(t *testing.T) {
	script := buildStartnetScript("10.0.0.1", "win11", 445, 8080, "Win11.iso", false)
	for _, want := range []string{
		"Adapters WinPE can see",
		"ipconfig /all",
		"Intel E1000",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("netfail 诊断里缺少 %q:\n%s", want, script)
		}
	}
	if strings.Index(script, "ipconfig /all") > strings.Index(script, ":netready") {
		t.Errorf("ipconfig 必须在 netfail 分支里、netready 之前:\n%s", script)
	}
}
