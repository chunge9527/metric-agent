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

	// 预编译正则，避免每次 CleanExpired 都重新编译（regexp 编译开销不小）
	pattern *regexp.Regexp
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
		// P1-1: 预编译正则，prefix 在构造时固定，无需每次清理重编译
		pattern: regexp.MustCompile(
			`^` + regexp.QuoteMeta(prefix) + `-(\d{4}-\d{2}-\d{2})-(\d+)\.log$`,
		),
	}
}

// CleanExpired 执行过期日志清理
// 扫描日志目录中匹配 {prefix}-yyyy-MM-dd-{seq}.log 的文件，
// 判断文件名中的日期，早于 (当前日期 - maxRetainDay) 的文件直接删除
// 返回：删除的文件数、删除失败的文件名列表（用于上层告警）
// P1-2: 删除失败不再静默吞掉，收集文件名返回给上层
func (c *LogCleaner) CleanExpired() (int, []string) {
	// 确保目录存在
	if _, err := os.Stat(c.logDir); err != nil {
		if os.IsNotExist(err) {
			return 0, nil // 目录不存在无需清理
		}
		return 0, []string{fmt.Sprintf("检查日志目录失败: %v", err)}
	}

	// 计算过期截止日期：today - maxRetainDay
	cutoffDate := time.Now().AddDate(0, 0, -c.maxRetainDay)
	cutoffDateStr := cutoffDate.Format(constant.LogRotateDateLayout)

	// 快速前置过滤用的前缀
	prefixDate := c.prefix + "-"

	entries, err := os.ReadDir(c.logDir)
	if err != nil {
		return 0, []string{fmt.Sprintf("读取日志目录失败: %v", err)}
	}

	deletedCount := 0
	var failedFiles []string

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()

		// 快速前置过滤：前缀和后缀不匹配直接跳过
		if !strings.HasPrefix(name, prefixDate) || !strings.HasSuffix(name, ".log") {
			continue
		}

		// 用预编译正则精确匹配并提取日期
		matches := c.pattern.FindStringSubmatch(name)
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
			failedFiles = append(failedFiles, name)
			continue
		}
		deletedCount++
	}

	return deletedCount, failedFiles
}
