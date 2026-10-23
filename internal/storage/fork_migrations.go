// [bootimus-fork L0] fork 自有的数据表迁移。
//
// 为什么不把 &models.ProxyConfig{} 直接插进 SQLiteStore / PostgresStore.AutoMigrate 的
// 上游参数列表里：那样每次上游往同一个列表加模型，我们都必然冲突。
// 实测：上游 0.1.78 往 sqlite.go 那个「整行一个 AutoMigrate」里加了
// DeploymentProfile / EnrolmentRule，rebase 到 download-proxy 这一提交就卡住
// （同一行两边都改，git 无法自动合并）。
//
// 做法：fork 的表在这里单独做一次 AutoMigrate；上游的 AutoMigrate 只在函数末尾多调一行
// forkMigrate(s.db)。上游以后怎么改它的列表（加模型、调顺序、拆函数）都影响不到我们，
// 因为我们的改动落在函数末尾这个独立 hunk 里，不在同一行。
package storage

import (
	"fmt"

	"gorm.io/gorm"

	"bootimus/internal/models"
)

// forkMigrate 应用 fork 自有的表。
// GORM 的 AutoMigrate 是幂等的：重复执行只会补缺失的列/索引，不会丢数据，
// 所以每次启动都调一次是安全的。
func forkMigrate(db *gorm.DB) error {
	if err := db.AutoMigrate(&models.ProxyConfig{}); err != nil {
		return fmt.Errorf("failed to run fork migrations: %w", err)
	}
	return nil
}
