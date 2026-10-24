package autoinstall

// [bootimus-fork L1] fork-patch: autoinstall-generator
// SHA-512 crypt 的正确性用 openssl passwd -6 生成的向量卡住：
// 哈希算错的表现是「装完系统登录不了」，比报错更难查。

import (
	"strings"
	"testing"
)

func TestSha512CryptVectors(t *testing.T) {
	cases := []struct{ password, salt, want string }{
		{"Hello world!", "saltstring",
			"$6$saltstring$svn8UoSVapNtMuq1ukKS4tPQd8iKwSMHWjl/O817G3uBnIFNjnQJuesI68u4OTLiBFdcbYEdFCoEOfaS35inz1"},
		{"Sup3rSecret!", "abcdefgh",
			"$6$abcdefgh$IDZuUabMIdgPaHZo1j2NnNplfPa8RA9WOaJSNnR0wlhjlpQICdFM0fjP.uO1dL5WjBVzEFXJE.pDn2v3Dhinh0"},
		{"short", "12345678",
			"$6$12345678$hDB2p8HWhkU2SB7meFmFx34Aex7iD7cbWKV0mgx8metydSjaKHbE2SjjwWd7jpDlREtMrXlCLrQB6BpQbUfQP."},
		{"a-very-long-password-that-exceeds-sixty-four-characters-0123456789ABCDEFGHIJ", "saltsalt",
			"$6$saltsalt$m6mGBlzN9YYO0Wqqy0MJGC.kJGwRad84sUzv/K9znr7f5R8zn/wYuEaGHZsA4mYs5KIqggMyP9115uqaB8JHq1"},
		{"密码测试", "zhongwen",
			"$6$zhongwen$wwkSZor2tjgRv3C8MBJ05yI1k5VcTb8/Afq2Cg/D/PtDyCSEyQJHfeBL0MuqiazQQT3t956f976L4skCe/cyA0"},
	}
	for _, c := range cases {
		if got := Sha512Crypt(c.password, c.salt); got != c.want {
			t.Errorf("Sha512Crypt(%q, %q)\n got %s\nwant %s", c.password, c.salt, got, c.want)
		}
	}
}

func TestHashPasswordShape(t *testing.T) {
	if HashPassword("") != "" {
		t.Error("empty password should stay empty")
	}
	h := HashPassword("Sup3rSecret!")
	if !strings.HasPrefix(h, "$6$") {
		t.Fatalf("expected a $6$ hash, got %q", h)
	}
	if len(strings.Split(h, "$")) != 4 {
		t.Fatalf("unexpected hash shape: %q", h)
	}
	if HashPassword("Sup3rSecret!") == h {
		t.Error("salt must be random per call")
	}
}
