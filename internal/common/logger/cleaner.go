// Package logger 全局结构化日志
// cleaner.go - PRD 5.6 过期日志清理：启动时 + 每次轮转后执行
package logger

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"metric-agent/internal/common/constant"
)

// LogCleaner 过期日志清理器
// PRD 5.6：仅清理本 Agent 产生的、匹配 {prefix}-yyyy-MM-dd-{seq}.log 命名规则的轮转文件
// 不删除目录内其他无关文件
type LogCleaner struct {
	logDir       string // 日志目录
	prefix       string // 文件名前缀
	maxRetainDay int    // 最大保留天数
}

// NewLogCleaner 创建过期日志清理器
// maxRetainDay <=0 时使用默认 30 天
func NewLogCleaner(logDir, prefix string, maxRetainDay int) *LogCleaner {
	days := maxRetainDay
	if days <= 0 {
		days = constant.LogMaxRetainDays
	}
	return &LogCleaner{
		logDir:       logDir,
		prefix:       prefix,
		maxRetainDay: days,
	}
}

// CleanExpired 执行过期日志清理
// 扫描日志目录中匹配 {prefix}-yyyy-MM-dd-{seq}.log 的文件，
// 判断文件名中的日期，早于 (当前日期 - maxRetainDay) 的文件直接删除
// 返回：删除的文件数、错误信息
func (c *LogCleaner) CleanExpired() (int, error) {
	// 确保目录存在
	if _, err := os.Stat(c.logDir); err != nil {
		if os.IsNotExist(err) {
			return 0, nil // 目录不存在无需清理
		}
		return 0, fmt.Errorf("检查日志目录失败: %w", err)
	}

	// 计算过期截止日期：today - maxRetainDay
	cutoffDate := time.Now().AddDate(0, 0, -c.maxRetainDay)
	cutoffDateStr := cutoffDate.Format(constant.LogRotateDateLayout)

	// 匹配规则：{prefix}-yyyy-MM-dd-{seq}.log
	// 先匹配前缀+日期部分，再从日期字符串解析
	prefixDate := c.prefix + "-"

	entries, err := os.ReadDir(c.logDir)
	if err != nil {
		return 0, fmt.Errorf("读取日志目录失败: %w", err)
	}

	// 编译正则：匹配 {prefix}-YYYY-MM-DD-NNN.log 格式
	pattern := regexp.MustCompile(
		`^` + regexp.QuoteMeta(c.prefix) + `-(\d{4}-\d{2}-\d{2})-(\d+)\.log$`,
	)

	deletedCount := 0

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()

		// 快速前置过滤：前缀和后缀不匹配直接跳过
		if !strings.HasPrefix(name, prefixDate) || !strings.HasSuffix(name, ".log") {
			continue
		}

		// 正则精确匹配并提取日期
		matches := pattern.FindStringSubmatch(name)
		if len(matches) != 3 {
			continue
		}
		fileDateStr := matches[1] // yyyy-MM-dd

		// 与截止日期比较（字符串按字典序比较等同于日期比较，因为格式是 YYYY-MM-DD）
		if fileDateStr >= cutoffDateStr {
			continue // 未过期
		}

		// 过期，删除
		filePath := filepath.Join(c.logDir, name)
		if err := os.Remove(filePath); err != nil {
			// 删除失败不中断，继续清理其他文件
			continue
		}
		deletedCount++
	}

	return deletedCount, nil
}
