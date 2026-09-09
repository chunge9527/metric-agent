// Package logger 全局结构化日志
// rotator.go - PRD 5.5 双触发轮转器：文件大小 OR 每日 0 点，任一条件满足即触发轮转
package logger

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"metric-agent/internal/common/constant"
)

// DailyRotator 双触发轮转日志写入器
// 实现 io.Writer 接口，可直接作为 slog.JSONHandler 的输出目标
// PRD 5.5 双触发：文件大小达到阈值 OR 每日 0 点，任一条件满足即轮转
type DailyRotator struct {
	// 配置项
	logDir      string // 日志目录
	prefix      string // 文件名前缀（如 metricAgent）
	maxSizeByte int64  // 单文件最大字节数
	onRotate    func() // 轮转后回调（如过期清理），可 nil

	// 运行时状态（需加锁保护）
	mu          sync.Mutex // 保护文件写入和轮转操作
	currentFile *os.File   // 当前活跃日志文件句柄
	currentSize int64      // 当前文件已写入字节数
	currentDate string     // 当前文件对应的日期 yyyy-MM-dd
	currentSeq  int        // 当前日期下的序号（跨天重置为 0）

	// 性能缓存：整数比较代替每次 time.Now().Format()
	// Write 热路径用 year + yearDay 整数比较判断是否跨天，仅真正跨天时才做 Format
	currentYear    int // currentDate 对应的年份
	currentYearDay int // currentDate 对应的一年中的第几天（1~366）

	// 性能缓存：避免每次 findMaxSeq 都 fmt.Sprintf 拼前缀
	prefixDash string // prefix + "-"，例如 "metricAgent-"

	// 后台协程控制
	stopCh  chan struct{} // 停止信号
	started bool          // 后台协程是否已启动
}

// NewDailyRotator 创建双触发轮转器
// logDir: 日志所在目录（必须已存在）
// prefix: 日志文件前缀，如 "metricAgent"
// maxSizeMB: 单文件最大尺寸（MB），<=0 时使用默认 100MB
// onRotate: 轮转完成后回调（如触发过期清理），可 nil
func NewDailyRotator(logDir, prefix string, maxSizeMB int, onRotate func()) (*DailyRotator, error) {
	// 确保目录存在
	if err := os.MkdirAll(logDir, constant.DefaultDirPerm); err != nil {
		return nil, fmt.Errorf("创建日志目录失败: %w", err)
	}

	// 计算文件大小阈值
	sizeMB := maxSizeMB
	if sizeMB <= 0 {
		sizeMB = constant.LogMaxSizeMB
	}

	r := &DailyRotator{
		logDir:      logDir,
		prefix:      prefix,
		maxSizeByte: int64(sizeMB) * 1024 * 1024,
		onRotate:    onRotate,
		stopCh:      make(chan struct{}),
		prefixDash:  prefix + "-",
	}

	// 初始化当前文件（启动时即判断是否需要跨天轮转）
	if err := r.initFile(); err != nil {
		return nil, err
	}

	return r, nil
}

// initFile 初始化当前活跃日志文件
// 启动时调用：
//  1. 检查目录中是否有今天的未轮转文件（{prefix}.log），继续追加
//  2. 如果没有今天的文件，创建新的 {prefix}.log 并记录今天日期
//  3. 如果发现今天已有轮转文件，找到当前最大序号的下一个
func (r *DailyRotator) initFile() error {
	now := time.Now()
	today := now.Format(constant.LogRotateDateLayout)

	// 优先检查根目录下是否有 {prefix}.log（还没轮转的活跃文件）
	activePath := filepath.Join(r.logDir, r.prefix+".log")
	if info, err := os.Stat(activePath); err == nil && info.Mode().IsRegular() {
		// 文件存在，检查是否是今天的（通过 mtime 或回退到创建新文件）
		// 为简化处理：只要文件存在且日期匹配今天，就继续追加；否则先轮转再新建
		if info.ModTime().Format(constant.LogRotateDateLayout) == today {
			return r.openActiveFile(activePath, info.Size(), now)
		}
		// 旧文件：先轮转走，再创建新的
		if err := r.rotateExisting(activePath); err != nil {
			return err
		}
	}

	// 没有活跃文件或旧文件已轮转，创建新的 today 文件
	return r.createNewActiveFile(now)
}

// openActiveFile 打开已存在的活跃日志文件继续追加
func (r *DailyRotator) openActiveFile(path string, size int64, now time.Time) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, constant.DefaultFilePerm)
	if err != nil {
		return fmt.Errorf("打开活跃日志文件失败: %w", err)
	}
	r.currentFile = f
	r.currentSize = size
	r.setDateCache(now)
	r.currentSeq = r.findMaxSeq(r.currentDate)
	return nil
}

// createNewActiveFile 创建新的 {prefix}.log 活跃文件
func (r *DailyRotator) createNewActiveFile(now time.Time) error {
	activePath := filepath.Join(r.logDir, r.prefix+".log")
	f, err := os.OpenFile(activePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, constant.DefaultFilePerm)
	if err != nil {
		return fmt.Errorf("创建活跃日志文件失败: %w", err)
	}
	r.currentFile = f
	r.currentSize = 0
	r.setDateCache(now)
	r.currentSeq = r.findMaxSeq(r.currentDate)
	return nil
}

// rotateExisting 将已有的活跃文件轮转走（重命名为 {prefix}-yyyy-MM-dd-{seq}.log）
// 轮转文件名使用文件 ModTime 的日期，而非当前时间——因为被轮转的文件内容属于它实际写入的日期
// 注意：此方法仅在 initFile 启动阶段调用，此时 currentFile 尚未初始化，无需关闭句柄
func (r *DailyRotator) rotateExisting(activePath string) error {
	// 获取文件实际修改时间作为轮转文件名的日期
	info, err := os.Stat(activePath)
	if err != nil {
		return fmt.Errorf("读取旧日志文件信息失败: %w", err)
	}
	contentDate := info.ModTime().Format(constant.LogRotateDateLayout)
	seq := r.findMaxSeq(contentDate) + 1

	rotatedName := fmt.Sprintf(constant.LogRotateFilePattern, r.prefix, contentDate, seq)
	rotatedPath := filepath.Join(r.logDir, rotatedName)

	if err := os.Rename(activePath, rotatedPath); err != nil {
		return fmt.Errorf("轮转旧日志文件失败: %w", err)
	}
	return nil
}

// findMaxSeq 查找指定日期下已有的最大序号
// 扫描目录中匹配 {prefix}-yyyy-MM-dd-*.log 的文件，返回最大序号（无匹配返回 -1）
func (r *DailyRotator) findMaxSeq(date string) int {
	entries, err := os.ReadDir(r.logDir)
	if err != nil {
		return -1
	}

	// 用预存的 prefixDash 拼前缀，避免每次 fmt.Sprintf 堆分配
	prefixDate := r.prefixDash + date + "-"
	maxSeq := -1

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, prefixDate) {
			continue
		}
		if !strings.HasSuffix(name, ".log") {
			continue
		}
		// 提取序号部分：metricAgent-2026-09-07-2.log → 2
		seqStr := strings.TrimSuffix(strings.TrimPrefix(name, prefixDate), ".log")
		var seq int
		if _, err := fmt.Sscanf(seqStr, "%d", &seq); err == nil {
			if seq > maxSeq {
				maxSeq = seq
			}
		}
	}

	return maxSeq
}

// setDateCache 更新 currentDate 及 year/yearDay 整数缓存
// 跨天只调用一次，Write 热路径只做整数比较，避免每次 time.Now().Format() 的堆分配
func (r *DailyRotator) setDateCache(t time.Time) {
	r.currentDate = t.Format(constant.LogRotateDateLayout)
	r.currentYear = t.Year()
	r.currentYearDay = t.YearDay()
}

// Write 实现 io.Writer 接口
// 每次写入后检查：文件大小是否超限 OR 是否已跨天，任一满足即触发轮转
func (r *DailyRotator) Write(p []byte) (n int, err error) {
	var needCallback bool
	r.mu.Lock()

	// 跨天检测：先用整数比较（0 alloc），只有真正跨天才 Format
	now := time.Now()
	if now.Year() != r.currentYear || now.YearDay() != r.currentYearDay {
		if err := r.doRotate(now); err != nil {
			r.mu.Unlock()
			return 0, err
		}
		needCallback = true
	}

	// 检查文件大小是否超限
	if r.currentSize+int64(len(p)) > r.maxSizeByte && r.maxSizeByte > 0 {
		if err := r.doRotate(now); err != nil {
			r.mu.Unlock()
			return 0, err
		}
		needCallback = true
	}

	// 写入
	if r.currentFile == nil {
		r.mu.Unlock()
		return 0, fmt.Errorf("日志文件未初始化")
	}
	n, err = r.currentFile.Write(p)
	if err == nil {
		r.currentSize += int64(n)
	}
	r.mu.Unlock()

	// 释放锁后触发回调（避免死锁）
	if needCallback && r.onRotate != nil {
		r.onRotate()
	}
	return n, err
}

// doRotate 执行轮转（内部调用，调用者需持有 r.mu）
// 逻辑：
//  1. Sync + Close 当前文件（先 Sync 再 Close 防止 OS 延迟写入缓存丢失最后一批日志）
//  2. 重命名为 {prefix}-date-{seq}.log（date 使用被轮转内容所属日期 r.currentDate，而非 newTime）
//  3. 如果日期变了（跨天），重置 seq 为 0；否则 seq+1
//  4. 创建新的 {prefix}.log
//     注意：本方法内部不调用 onRotate 回调——回调由调用方在释放锁后触发，避免死锁
func (r *DailyRotator) doRotate(newTime time.Time) error {
	newDate := newTime.Format(constant.LogRotateDateLayout)

	// 关闭当前文件（P0-1: 轮转时必须先 Sync 再 Close，防止 crash 丢最后一批日志）
	if r.currentFile != nil {
		_ = r.currentFile.Sync()
		if err := r.currentFile.Close(); err != nil {
			// 关闭失败不阻断轮转，继续尝试重命名
		}
		r.currentFile = nil
	}

	activePath := filepath.Join(r.logDir, r.prefix+".log")

	// 轮转文件名应使用【被轮转内容所属日期】，而非轮转发生的日期
	// 跨天轮转时：r.currentDate 是旧日期（内容实际归属的日期），newDate 是新日期
	rotatedDate := r.currentDate
	if rotatedDate == "" {
		// 启动时 currentDate 还未初始化，回退用 newDate
		rotatedDate = newDate
	}

	// 计算新序号（基于 rotatedDate 扫描已有轮转文件）
	var newSeq int
	if newDate == r.currentDate {
		// 同一天轮转，序号 +1
		newSeq = r.currentSeq + 1
	} else {
		// 跨天轮转：被轮转的内容属于 rotatedDate（旧日期），
		// 序号应基于 rotatedDate 扫描已有文件后 +1
		newSeq = r.findMaxSeq(rotatedDate)
		if newSeq < 0 {
			newSeq = 0
		} else {
			newSeq++
		}
	}

	// 重命名活跃文件为轮转文件（使用内容所属日期 rotatedDate）
	rotatedName := fmt.Sprintf(constant.LogRotateFilePattern, r.prefix, rotatedDate, newSeq)
	rotatedPath := filepath.Join(r.logDir, rotatedName)

	// P2-1: 直接 Rename，不再先 Stat 多余 syscall
	// 活跃文件在启动时才可能不存在；正常运行时一定存在
	if err := os.Rename(activePath, rotatedPath); err != nil {
		// 启动首次轮转时可能文件还没创建，不是错误
		if !os.IsNotExist(err) {
			return fmt.Errorf("轮转日志失败: %w", err)
		}
	}

	// 更新状态为新日期（新活跃文件的日期）及 year/day 整数缓存
	r.setDateCache(newTime)
	r.currentSeq = newSeq

	// 创建新的活跃文件
	f, err := os.OpenFile(activePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, constant.DefaultFilePerm)
	if err != nil {
		return fmt.Errorf("创建新日志文件失败: %w", err)
	}
	r.currentFile = f
	r.currentSize = 0

	return nil
}

// StartDailyCheck 启动后台跨天检测 goroutine
// 使用一次性 timer 计算到下一个 0 点，而非每秒 ticker，避免 CPU 浪费
// 幂等：重复调用只会启动一个 goroutine
func (r *DailyRotator) StartDailyCheck() {
	r.mu.Lock()
	if r.started {
		r.mu.Unlock()
		return
	}
	r.started = true
	r.stopCh = make(chan struct{})
	r.mu.Unlock()

	go r.dailyCheckLoop()
}

// dailyCheckLoop 后台跨天检测循环
// 使用 time.NewTimer 等待到下一个 0 点（本地时区），
// 比 time.After 更可控（可主动 Stop 释放 timer），比 1s ticker 高效得多
func (r *DailyRotator) dailyCheckLoop() {
	for {
		// 计算到下一个本地时区午夜的时间
		now := time.Now()
		nextMidnight := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
		wait := time.Until(nextMidnight)

		// P2-2: 用 NewTimer 代替 time.After，Stop 时能主动释放 timer
		timer := time.NewTimer(wait)
		select {
		case <-r.stopCh:
			timer.Stop()
			return
		case <-timer.C:
			// 到达 0 点，触发轮转
			// 加守卫：Write 可能已经抢先处理了跨天（goroutine 调度延迟时），避免重复轮转
			r.mu.Lock()
			now := time.Now()
			today := now.Format(constant.LogRotateDateLayout)
			var rotateErr error
			if r.currentDate != today {
				// Write 还没处理跨天，goroutine 来兜底
				rotateErr = r.doRotate(now)
			}
			// else: Write 已经抢先 doRotate 过了，currentDate 已是今天，跳过避免产生冗余空轮转文件
			r.mu.Unlock()

			// 释放锁后触发回调（避免死锁）
			if rotateErr == nil && r.onRotate != nil {
				r.onRotate()
			}
		}
	}
}

// Stop 停止后台协程并刷新、关闭文件句柄
// 应在程序退出前调用一次
func (r *DailyRotator) Stop() error {
	r.mu.Lock()
	// 停止后台协程
	if r.started {
		close(r.stopCh)
		r.started = false
	}
	// 关闭文件
	var err error
	if r.currentFile != nil {
		err = r.currentFile.Sync()
		_ = r.currentFile.Close()
		r.currentFile = nil
	}
	r.mu.Unlock()
	return err
}

// RotateNow 立即触发一次轮转（供外部调用，如过期清理后想立即刷新序号）
func (r *DailyRotator) RotateNow() error {
	r.mu.Lock()
	err := r.doRotate(time.Now())
	r.mu.Unlock()

	// 释放锁后触发回调
	if err == nil && r.onRotate != nil {
		r.onRotate()
	}
	return err
}

// LogFileInfo 当前活跃日志文件信息（调试/监控用）
type LogFileInfo struct {
	Path      string
	Size      int64
	Date      string
	Seq       int
	MaxSizeMB int
}

// Info 返回当前活跃文件的信息快照
func (r *DailyRotator) Info() LogFileInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	return LogFileInfo{
		Path:      filepath.Join(r.logDir, r.prefix+".log"),
		Size:      r.currentSize,
		Date:      r.currentDate,
		Seq:       r.currentSeq,
		MaxSizeMB: int(r.maxSizeByte / 1024 / 1024),
	}
}

// ensureDailyRotator implements io.Writer
var _ io.Writer = (*DailyRotator)(nil)
