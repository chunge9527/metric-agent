// Package logger 全局结构化日志
// logger_test.go - PRD 5.8 单元测试：级别过滤、轮转命名、目录创建、双输出、跨天、清理
package logger

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"metric-agent/internal/common/constant"
	"metric-agent/internal/model"
)

// testTempDir 创建测试专用临时目录，返回路径和清理函数
func testTempDir(t *testing.T) (string, func()) {
	t.Helper()
	dir, err := os.MkdirTemp("", "logger_test_*")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	return dir, func() { os.RemoveAll(dir) }
}

// ============ 测试1：日志级别过滤 ============

// TestLogLevelFilterDebug 验证 debug 级别时全部输出
func TestLogLevelFilterDebug(t *testing.T) {
	dir, cleanup := testTempDir(t)
	defer cleanup()

	cfg := model.LogConfig{Level: "debug", MaxFileSize: 100, MaxRetainDays: 30}
	logPath := filepath.Join(dir, "test.log")

	if err := InitFileLogger(logPath, cfg); err != nil {
		t.Fatalf("InitFileLogger 失败: %v", err)
	}
	defer Close()

	Debug("debug_msg")
	Info("info_msg")
	Warn("warn_msg")
	Error("error_msg")

	// 读取文件内容验证四条都写入
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("读取日志文件失败: %v", err)
	}
	content := string(data)
	for _, want := range []string{"debug_msg", "info_msg", "warn_msg", "error_msg"} {
		if !strings.Contains(content, want) {
			t.Errorf("期望日志包含 %q，实际: %s", want, content)
		}
	}
}

// TestLogLevelFilterInfo 验证 info 级别丢弃 debug
func TestLogLevelFilterInfo(t *testing.T) {
	dir, cleanup := testTempDir(t)
	defer cleanup()

	cfg := model.LogConfig{Level: "info", MaxFileSize: 100, MaxRetainDays: 30}
	logPath := filepath.Join(dir, "test.log")

	if err := InitFileLogger(logPath, cfg); err != nil {
		t.Fatalf("InitFileLogger 失败: %v", err)
	}
	defer Close()

	Debug("debug_msg")
	Info("info_msg")
	Warn("warn_msg")

	data, _ := os.ReadFile(logPath)
	content := string(data)
	if strings.Contains(content, "debug_msg") {
		t.Error("info 级别应丢弃 debug 日志")
	}
	if !strings.Contains(content, "info_msg") {
		t.Error("info 级别应输出 info 日志")
	}
	if !strings.Contains(content, "warn_msg") {
		t.Error("info 级别应输出 warn 日志")
	}
}

// TestLogLevelFilterError 验证 error 级别只输出 error
func TestLogLevelFilterError(t *testing.T) {
	dir, cleanup := testTempDir(t)
	defer cleanup()

	cfg := model.LogConfig{Level: "error", MaxFileSize: 100, MaxRetainDays: 30}
	logPath := filepath.Join(dir, "test.log")

	if err := InitFileLogger(logPath, cfg); err != nil {
		t.Fatalf("InitFileLogger 失败: %v", err)
	}
	defer Close()

	Debug("debug_msg")
	Info("info_msg")
	Warn("warn_msg")
	Error("error_msg")

	data, _ := os.ReadFile(logPath)
	content := string(data)
	if strings.Contains(content, "debug_msg") || strings.Contains(content, "info_msg") || strings.Contains(content, "warn_msg") {
		t.Error("error 级别只应输出 error 日志")
	}
	if !strings.Contains(content, "error_msg") {
		t.Error("error 级别应输出 error 日志")
	}
}

// TestLogLevelIllegalDefault 验证非法级别回退为 info
func TestLogLevelIllegalDefault(t *testing.T) {
	dir, cleanup := testTempDir(t)
	defer cleanup()

	cfg := model.LogConfig{Level: "invalid_level", MaxFileSize: 100, MaxRetainDays: 30}
	logPath := filepath.Join(dir, "test.log")

	if err := InitFileLogger(logPath, cfg); err != nil {
		t.Fatalf("InitFileLogger 失败: %v", err)
	}
	defer Close()

	// 非法级别回退 info，debug 应被丢弃
	Debug("debug_msg")
	Info("info_msg")

	data, _ := os.ReadFile(logPath)
	content := string(data)
	if strings.Contains(content, "debug_msg") {
		t.Error("非法级别应回退 info，debug 应被丢弃")
	}
	if !strings.Contains(content, "info_msg") {
		t.Error("info 应正常输出")
	}
}

// ============ 测试2：轮转文件命名格式 ============

// TestRotateFileNameFormat 验证轮转文件命名符合 {prefix}-yyyy-MM-dd-{seq}.log
func TestRotateFileNameFormat(t *testing.T) {
	dir, cleanup := testTempDir(t)
	defer cleanup()

	cfg := model.LogConfig{Level: "info", MaxFileSize: 1, MaxRetainDays: 30} // 1MB 便于快速触发轮转
	logPath := filepath.Join(dir, "myApp.log")

	if err := InitFileLogger(logPath, cfg); err != nil {
		t.Fatalf("InitFileLogger 失败: %v", err)
	}
	defer Close()

	// 写入超过 1MB 的数据触发轮转
	bigMsg := strings.Repeat("x", 1024*1024) // 1MB
	for i := 0; i < 3; i++ {
		Info(bigMsg)
	}

	// 等待轮转完成
	time.Sleep(200 * time.Millisecond)

	// 扫描目录验证轮转文件命名
	entries, _ := os.ReadDir(dir)
	dateStr := time.Now().Format(constant.LogRotateDateLayout)
	pattern := regexp.MustCompile(`^myApp-` + regexp.QuoteMeta(dateStr) + `-\d+\.log$`)

	var rotatedCount int
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if name == "myApp.log" {
			continue // 活跃文件不算轮转
		}
		if pattern.MatchString(name) {
			rotatedCount++
			t.Logf("找到轮转文件: %s", name)
		} else {
			t.Errorf("不符合命名格式的文件: %s（期望 myApp-%s-N.log）", name, dateStr)
		}
	}

	if rotatedCount == 0 {
		t.Error("未发现任何轮转文件，可能轮转未触发")
	}
}

// ============ 测试3：目录自动创建 ============

// TestDirAutoCreate 验证日志目录不存在时自动创建
func TestDirAutoCreate(t *testing.T) {
	parentDir, cleanup := testTempDir(t)
	defer cleanup()

	// 嵌套不存在的目录
	nestedLogDir := filepath.Join(parentDir, "sub", "nested", "logs")
	logPath := filepath.Join(nestedLogDir, "autoCreate.log")

	cfg := model.LogConfig{Level: "info", MaxFileSize: 100, MaxRetainDays: 30}
	if err := InitFileLogger(logPath, cfg); err != nil {
		t.Fatalf("InitFileLogger 应自动创建目录，却失败: %v", err)
	}
	defer Close()

	Info("test_msg")

	if _, err := os.Stat(nestedLogDir); err != nil {
		t.Errorf("目录未被自动创建: %v", err)
	}
	if _, err := os.Stat(logPath); err != nil {
		t.Errorf("日志文件未被创建: %v", err)
	}
}

// ============ 测试4：双输出验证 ============

// TestDualOutput 验证 stdout 和文件都有输出
func TestDualOutput(t *testing.T) {
	dir, cleanup := testTempDir(t)
	defer cleanup()

	cfg := model.LogConfig{Level: "info", MaxFileSize: 100, MaxRetainDays: 30}
	logPath := filepath.Join(dir, "dual.log")

	// 重定向 stdout 捕获输出
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	if err := InitFileLogger(logPath, cfg); err != nil {
		os.Stdout = oldStdout
		t.Fatalf("InitFileLogger 失败: %v", err)
	}
	defer Close()

	Info("dual_test_msg")

	// 恢复 stdout 并读取捕获
	w.Close()
	os.Stdout = oldStdout
	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	stdoutContent := string(buf[:n])

	// 文件内容
	fileData, _ := os.ReadFile(logPath)
	fileContent := string(fileData)

	// 两边都要有
	if !strings.Contains(stdoutContent, "dual_test_msg") {
		t.Error("stdout 应包含日志内容")
	}
	if !strings.Contains(fileContent, "dual_test_msg") {
		t.Error("文件应包含日志内容")
	}
}

// ============ 测试5：跨天轮转序号重置 ============

// TestCrossDaySeqReset 验证跨天后序号重置为 0（通过模拟手动创建前一天的轮转文件）
func TestCrossDaySeqReset(t *testing.T) {
	dir, cleanup := testTempDir(t)
	defer cleanup()

	prefix := "agent"
	yesterday := time.Now().AddDate(0, 0, -1).Format(constant.LogRotateDateLayout)
	today := time.Now().Format(constant.LogRotateDateLayout)

	// 模拟昨天已有 2 个轮转文件
	for i := 0; i <= 1; i++ {
		name := filepath.Join(dir, prefix+"-"+yesterday+"-"+itoa(i)+".log")
		os.WriteFile(name, []byte("yesterday_data"), 0644)
	}

	// 今天已有一个轮转文件
	todayFile := filepath.Join(dir, prefix+"-"+today+"-0.log")
	os.WriteFile(todayFile, []byte("today_data"), 0644)

	// 初始化轮转器（低阈值快速触发）
	rotator, err := NewDailyRotator(dir, prefix, 1, nil) // 1MB 阈值，无回调
	if err != nil {
		t.Fatalf("NewDailyRotator 失败: %v", err)
	}
	defer rotator.Stop()

	// 验证 findMaxSeq 能正确找到今天已有的 seq
	maxSeq := rotator.findMaxSeq(today)
	if maxSeq != 0 {
		t.Errorf("今天已有 seq=0，findMaxSeq 应返回 0，实际返回 %d", maxSeq)
	}

	yesterdaySeq := rotator.findMaxSeq(yesterday)
	if yesterdaySeq != 1 {
		t.Errorf("昨天已有 seq=0,1，findMaxSeq 应返回 1，实际返回 %d", yesterdaySeq)
	}
}

// ============ 测试6：过期日志清理 ============

// TestCleanExpired 验证过期日志清理逻辑
func TestCleanExpired(t *testing.T) {
	dir, cleanup := testTempDir(t)
	defer cleanup()

	prefix := "agent"

	// 创建过期日志（60 天前的日期）
	oldDate := time.Now().AddDate(0, 0, -60).Format(constant.LogRotateDateLayout)
	for i := 0; i < 2; i++ {
		name := filepath.Join(dir, prefix+"-"+oldDate+"-"+itoa(i)+".log")
		os.WriteFile(name, []byte("old_data"), 0644)
	}

	// 创建近期日志（10 天前，未过期）
	recentDate := time.Now().AddDate(0, 0, -10).Format(constant.LogRotateDateLayout)
	recentFile := filepath.Join(dir, prefix+"-"+recentDate+"-0.log")
	os.WriteFile(recentFile, []byte("recent_data"), 0644)

	// 创建今天的日志
	todayDate := time.Now().Format(constant.LogRotateDateLayout)
	todayFile := filepath.Join(dir, prefix+"-"+todayDate+"-0.log")
	os.WriteFile(todayFile, []byte("today_data"), 0644)

	// 创建无关文件（不应被清理）
	otherFile := filepath.Join(dir, "other_file.log")
	os.WriteFile(otherFile, []byte("other"), 0644)

	// 执行清理（保留 30 天）
	cleaner := NewLogCleaner(dir, prefix, 30)
	deleted, err := cleaner.CleanExpired()
	if err != nil {
		t.Fatalf("CleanExpired 失败: %v", err)
	}

	if deleted != 2 {
		t.Errorf("应删除 2 个过期文件，实际删除 %d 个", deleted)
	}

	// 验证未过期文件仍在
	if _, err := os.Stat(recentFile); err != nil {
		t.Error("近期日志不应被删除")
	}
	if _, err := os.Stat(todayFile); err != nil {
		t.Error("今天日志不应被删除")
	}
	if _, err := os.Stat(otherFile); err != nil {
		t.Error("无关文件不应被删除")
	}
}

// ============ 测试7：双输出 DailyRotator 基本功能 ============

// TestDailyRotatorBasic 验证轮转器基本写入功能
func TestDailyRotatorBasic(t *testing.T) {
	dir, cleanup := testTempDir(t)
	defer cleanup()

	rotator, err := NewDailyRotator(dir, "testApp", 100, nil)
	if err != nil {
		t.Fatalf("NewDailyRotator 失败: %v", err)
	}
	defer rotator.Stop()

	// 写入
	testData := []byte("hello world\n")
	n, err := rotator.Write(testData)
	if err != nil {
		t.Fatalf("Write 失败: %v", err)
	}
	if n != len(testData) {
		t.Errorf("写入字节数不符: 期望 %d, 实际 %d", len(testData), n)
	}

	// 验证文件存在且内容正确
	activePath := filepath.Join(dir, "testApp.log")
	data, err := os.ReadFile(activePath)
	if err != nil {
		t.Fatalf("读取活跃文件失败: %v", err)
	}
	if string(data) != string(testData) {
		t.Errorf("文件内容不符: 期望 %q, 实际 %q", testData, data)
	}

	// 验证 Info()
	info := rotator.Info()
	if info.Size != int64(len(testData)) {
		t.Errorf("Info().Size 不符: 期望 %d, 实际 %d", len(testData), info.Size)
	}
}

// ============ 辅助函数 ============

// itoa 简单的 int 转 string（避免 import strconv）
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// ============ 日志级别解析测试 ============

// TestParseSlogLevel 验证级别字符串解析
func TestParseSlogLevel(t *testing.T) {
	tests := []struct {
		input string
		want  int // slog.Level 值
	}{
		{"debug", -4},
		{"INFO", 0},
		{"Warn", 4},
		{"ERROR", 8},
		{"invalid", 0},
		{"", 0},
	}

	for _, tt := range tests {
		got := parseSlogLevel(tt.input)
		if int(got) != tt.want {
			t.Errorf("parseSlogLevel(%q) = %d, 期望 %d", tt.input, got, tt.want)
		}
	}
}

// ============ 前缀提取测试 ============

// TestExtractPrefix 验证日志文件前缀提取
func TestExtractPrefix(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"./logs/metricAgent.log", "metricAgent"},
		{"/home/cib/agent/logs/app.log", "app"},
		{"myLog.log", "myLog"},
	}

	for _, tt := range tests {
		got := extractPrefix(tt.input)
		if got != tt.want {
			t.Errorf("extractPrefix(%q) = %q, 期望 %q", tt.input, got, tt.want)
		}
	}
}
