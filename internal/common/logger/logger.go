// Package logger 全局结构化日志
// logger.go - PRD 5.1~5.7：统一 JSON 结构化日志、双输出、级别可配、优雅关闭
package logger

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"metric-agent/internal/model"
)

// ============ 全局状态 ============

var (
	globalLogger  *slog.Logger
	globalRotator *DailyRotator
	globalCleaner *LogCleaner
	globalLevel   *slog.LevelVar // 支持运行时动态调整级别
	closeOnce     sync.Once      // 保证 Close 只执行一次
	initMu        sync.Mutex     // 保护 InitFileLogger 的热重载原子性
)

// ============ 初始化 ============

// InitFileLogger 初始化 HTTP 服务模式的文件日志
// PRD 5.3：同时输出到 stdout + 本地日志文件，双通道并行生效
// PRD 5.4：logPath 完整路径（含目录和文件名），从中提取目录和前缀
// PRD 5.6：每次轮转完成后触发过期清理
// PRD 5.7：cfg 提供级别、单文件大小、保留天数配置
// 同时启动后台跨天检测 goroutine，并执行首次过期清理
// P1-4: 支持热重载——第二次调用会先 Stop 旧 rotator 再创建新的，防止 goroutine 泄漏
//
//	同时重置 closeOnce，让下一次 Close 能正确执行（sync.Once 不能 reset）
func InitFileLogger(logPath string, cfg model.LogConfig) error {
	initMu.Lock()
	defer initMu.Unlock()

	// 解析路径
	logDir := filepath.Dir(logPath)
	prefix := extractPrefix(logPath)

	// P1-4: 热重载保护——先停旧的，防止旧 goroutine 继续写同一个文件
	if globalRotator != nil {
		_ = globalRotator.Stop()
		globalRotator = nil
	}
	// 重置 closeOnce：Close 是 sync.Once，第一次 Close 后 Done 标记永久为 true
	// 热重载时必须重置才能让下一次 Close 生效
	closeOnce = sync.Once{}

	// 创建清理器（轮转后需要用到）
	cleaner := NewLogCleaner(logDir, prefix, cfg.MaxRetainDays)
	globalCleaner = cleaner

	// 创建轮转器，注册轮转回调（每次轮转后执行过期清理，满足 PRD 5.6）
	rotator, err := NewDailyRotator(logDir, prefix, cfg.MaxFileSize, func() {
		if cleaner != nil {
			_, _ = cleaner.CleanExpired() // 轮转回调里的清理失败不需要打日志（避免递归调 logger）
		}
	})
	if err != nil {
		return err
	}
	globalRotator = rotator

	// 构建 multi-writer：stdout + DailyRotator（双输出）
	multiWriter := newMultiWriter(os.Stdout, rotator)

	// 根据配置设置日志级别
	levelVar := &slog.LevelVar{}
	levelVar.Set(parseSlogLevel(cfg.Level))
	globalLevel = levelVar

	// 直接使用 slog.JSONHandler（简洁无冗余字段）
	jsonHandler := slog.NewJSONHandler(multiWriter, &slog.HandlerOptions{
		Level: levelVar,
	})
	globalLogger = slog.New(jsonHandler)

	// 启动后台跨天检测 goroutine
	rotator.StartDailyCheck()

	// 启动时执行首次过期日志清理（忽略错误，清理失败不影响启动）
	// 满足 PRD 5.6：启动时 + 每次轮转后都执行清理
	_, _ = cleaner.CleanExpired()

	return nil
}

// ============ 日志级别解析 ============

// parseSlogLevel 将 yaml 字符串级别转为 slog.Level
// 非法值回退到 slog.LevelInfo
func parseSlogLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// SetLogLevel 运行时动态调整日志级别
// 传入非法值时回退到 info
func SetLogLevel(level string) {
	if globalLevel != nil {
		globalLevel.Set(parseSlogLevel(level))
	}
}

// ============ 文件前缀提取 ============

// extractPrefix 从日志完整路径中提取文件前缀
// 规则：取文件名（含扩展名）去掉 ".log" 后缀后的部分
// 示例：./logs/metricAgent.log → metricAgent
//
//	/home/cib/agent/logs/app.log → app
func extractPrefix(logPath string) string {
	base := filepath.Base(logPath)
	if idx := strings.LastIndex(base, "."); idx >= 0 {
		return base[:idx]
	}
	return base
}

// ============ multi-writer ============

// multiWriter 多目标写入器，实现 io.Writer
type multiWriter struct {
	writers []io.Writer
}

func newMultiWriter(writers ...io.Writer) *multiWriter {
	return &multiWriter{writers: writers}
}

// Write 依次写入所有 writer，P0-3 修复：某个 writer 失败不中断整条日志
// 场景：stdout 正常输出，rotator 因磁盘满失败——不能让整条日志静默丢失
// 策略：全部写完后返回最后一个遇到的 error，让上层知道有 writer 失败但至少 stdout 已输出
func (mw *multiWriter) Write(p []byte) (n int, err error) {
	var firstErr error
	for _, w := range mw.writers {
		if w == nil {
			continue
		}
		wn, werr := w.Write(p)
		if werr != nil {
			// 记录第一个错误但继续写其他 writer（让日志至少到达 stdout）
			if firstErr == nil {
				firstErr = werr
			}
		}
		if wn > n {
			n = wn
		}
	}
	if firstErr != nil {
		// 返回第一个错误让 slog 知道有问题，但 stdout 等其他 writer 已经输出过了
		return n, firstErr
	}
	return len(p), nil
}

// ============ 对外日志 API ============

// Debug 输出 DEBUG 级别日志
func Debug(msg string, args ...any) {
	if globalLogger != nil {
		globalLogger.Debug(msg, args...)
	}
}

// Info 输出 INFO 级别日志
func Info(msg string, args ...any) {
	if globalLogger != nil {
		globalLogger.Info(msg, args...)
	}
}

// Warn 输出 WARN 级别日志
func Warn(msg string, args ...any) {
	if globalLogger != nil {
		globalLogger.Warn(msg, args...)
	}
}

// Error 输出 ERROR 级别日志
func Error(msg string, args ...any) {
	if globalLogger != nil {
		globalLogger.Error(msg, args...)
	}
}

// GetLogger 获取全局日志实例（供高级场景使用）
func GetLogger() *slog.Logger {
	return globalLogger
}

// ============ 优雅关闭 ============

// Close 关闭轮转器后台协程，同步刷新并关闭文件句柄
// 同时执行一次过期日志清理
// 幂等：多次调用只会执行一次
// 应在程序退出前（收到信号触发优雅关闭时）调用
func Close() {
	closeOnce.Do(func() {
		// 先执行一次过期清理
		if globalCleaner != nil {
			deleted, failed := globalCleaner.CleanExpired()
			if globalLogger != nil {
				if len(failed) > 0 {
					globalLogger.Warn("关闭前过期日志清理部分失败", "failedCount", len(failed), "failedFiles", strings.Join(failed, ","))
				}
				if deleted > 0 {
					globalLogger.Info("关闭前过期日志清理完成", "deleted", deleted)
				}
			}
		}

		// 停止轮转器（停止后台协程 + Sync + Close 文件）
		if globalRotator != nil {
			_ = globalRotator.Stop()
			globalRotator = nil
		}
	})
}

// TriggerClean 手动触发一次过期日志清理（供外部调用，如轮转后）
// 返回：删除的文件数、删除失败的文件名列表
func TriggerClean() (int, []string) {
	if globalCleaner != nil {
		return globalCleaner.CleanExpired()
	}
	return 0, nil
}

// RotateNow 手动触发一次日志轮转（供外部调用）
func RotateNow() error {
	if globalRotator != nil {
		return globalRotator.RotateNow()
	}
	return nil
}
