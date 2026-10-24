package autoinstall

// [bootimus-fork L1] fork-patch: autoinstall-generator
//
// 安装器要的是 crypt(3) 哈希：preseed 的 passwd/*-password-crypted、kickstart 的
// --iscrypted、Ubuntu autoinstall 的 identity.password 都按 $6$（sha512-crypt）写，
// 这也是 bootimus 官方文档示例里用的格式。Go 标准库没有 sha512-crypt，
// 这里用 github.com/GehirnInc/crypt 的 sha512_crypt 实现（纯 Go、无传递依赖），
// 并用 openssl passwd -6 生成的向量做回归。

import (
	"strings"

	sha512crypt "github.com/GehirnInc/crypt/sha512_crypt"
)

// Sha512Crypt hashes password with an explicit salt (with or without the $6$
// prefix). Returns "" on failure so callers can fall back instead of writing a
// broken hash into an answer file.
func Sha512Crypt(password, salt string) string {
	spec := salt
	if !strings.HasPrefix(spec, "$6$") {
		spec = "$6$" + spec
	}
	out, err := sha512crypt.New().Generate([]byte(password), []byte(spec))
	if err != nil {
		return ""
	}
	return out
}

// HashPassword hashes a password for an answer file, with a fresh random salt
// (the library generates it). Empty input stays empty so templates can decide
// whether to emit the directive at all.
func HashPassword(password string) string {
	if password == "" {
		return ""
	}
	out, err := sha512crypt.New().Generate([]byte(password), nil)
	if err != nil {
		return ""
	}
	return out
}
