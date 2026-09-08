package service

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	myconstant "metric-agent/internal/common/constant"
	"metric-agent/internal/common/logger"
	"metric-agent/internal/infra/iface"
	"metric-agent/internal/model"

	"gopkg.in/yaml.v3"
)

// GuardianService 进程守护服务（PRD 3.4）
type GuardianService struct {
	se         iface.ShellExecutor
	configPath string

	// 巡检周期（分钟）；默认 1 分钟，可通过 crontab.interval 配置（PRD 3.4.1）
	interval int
	// 超时配置（从 metricAgent.yml 读取）
	healthCheckTimeout int
	startScriptTimeout int

	mu         sync.Mutex // 保护 running / stopCtx / stopCancel
	lastCfg    model.GuardianConfigList
	stopCtx    context.Context
	stopCancel context.CancelFunc
	running    bool

	// wg 等待 guardianLoop 协程退出；Stop 时 cancel context 后需 Wait 确保协程真正收敛
	wg sync.WaitGroup

	// 防重叠：上一轮未完成则跳过
	executing bool
	execMu    sync.Mutex

	// ============ 暂停控制 ============
	// paused 暂停标记：true 时 guardianLoop 协程仍存活但跳过 inspect 执行
	// 使用 atomic 保证多线程（HTTP Handler vs guardianLoop）安全读写
	paused atomic.Bool

	// lastAction / lastActionAt 记录最近一次控制操作（start/pause/resume/stop）
	// 供 Status() 方法返回，便于运维查询
	statusMu     sync.Mutex
	lastAction   string
	lastActionAt int64
}

// NewGuardianService 创建进程守护服务
// interval: 巡检周期（分钟），未配置或 <=0 时默认 1 分钟
func NewGuardianService(se iface.ShellExecutor, configPath string, interval, healthCheckTimeout, startScriptTimeout int) *GuardianService {
	if interval <= 0 {
		interval = 1
	}
	if healthCheckTimeout <= 0 {
		healthCheckTimeout = myconstant.DefaultHealthCheckTimeout
	}
	if startScriptTimeout <= 0 {
		startScriptTimeout = myconstant.DefaultStartScriptTimeout
	}
	return &GuardianService{
		se:                 se,
		configPath:         configPath,
		interval:           interval,
		healthCheckTimeout: healthCheckTimeout,
		startScriptTimeout: startScriptTimeout,
		stopCtx:            context.Background(), // 默认根 ctx，确保 Stop 前 performHealthCheck 等方法也能正常派生超时 ctx
	}
}

// Start 启动守护巡检协程
// BUG-1 修复：删除 stopOnce，用 mu 保护避免重复启动导致的 panic
func (g *GuardianService) Start() {
	g.mu.Lock()
	if g.running {
		g.mu.Unlock()
		return
	}
	// 先 cancel 掉旧 ctx（防止上一次 Stop 没调用过 stopCancel 的边界情况）
	if g.stopCancel != nil {
		g.stopCancel()
	}
	// BUG-2 修复：根 context，所有 Shell Exec 的 ctx 从此派生；Stop 时 cancel 即可中断在途脚本
	g.stopCtx, g.stopCancel = context.WithCancel(context.Background())
	g.running = true
	g.wg.Add(1) // 注册协程，Stop 时 Wait
	g.mu.Unlock()

	// 重置暂停状态（Start 后默认运行中）
	g.paused.Store(false)
	g.setLastAction("start")

	go g.guardianLoop()
	logger.Info("进程守护服务已启动", "时间间隔", g.interval, "单位", "分钟")
}

// Stop 停止守护巡检
// BUG-1 修复：删除 stopOnce，mu 锁内判断 running 避免重复 cancel
// 新增：先释放 g.mu 再 wg.Wait()，避免 guardianLoop 内部如果需要获取 g.mu 时死锁
func (g *GuardianService) Stop() {
	g.mu.Lock()
	if !g.running {
		g.mu.Unlock()
		return
	}
	if g.stopCancel != nil {
		g.stopCancel()
		g.stopCancel = nil
	}
	g.running = false
	g.mu.Unlock() // 先释放锁，再 Wait，避免 guardianLoop 内部尝试获取 g.mu 时死锁

	// 重置暂停状态 + 记录停止操作（在 Wait 前，避免并发读取状态时遗漏）
	g.paused.Store(false)
	g.setLastAction("stop")

	g.wg.Wait() // 等待 guardianLoop 真正退出
	logger.Info("进程守护服务已停止")
}

// guardianLoop 守护巡检主循环（PRD 3.4.1）
// BUG-2 修复：用 g.stopCtx.Done() 替代 stopCh，Stop 时立即退出
// 暂停控制：ticker 照常触发，但 paused=true 时跳过 inspect，协程保持存活
func (g *GuardianService) guardianLoop() {
	defer g.wg.Done() // 确保 Stop 时 Wait 能等到协程退出

	ticker := time.NewTicker(time.Duration(g.interval) * time.Minute)
	defer ticker.Stop()

	// 启动后立即执行一次（除非暂停中）
	if !g.paused.Load() {
		g.safeInspect()
	} else {
		logger.Info("进程守护服务已处于暂停状态，跳过首次巡检")
	}

	for {
		select {
		case <-g.stopCtx.Done():
			return
		case <-ticker.C:
			if !g.paused.Load() {
				g.safeInspect()
			} else {
				logger.Warn("守护巡检已暂停，跳过本轮")
			}
		}
	}
}

// safeInspect 带 panic recover 的 inspect 包装
func (g *GuardianService) safeInspect() {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("守护巡检 panic 已捕获，本轮跳过，下次继续", "panic", fmt.Sprint(r))
		}
	}()
	g.inspect()
}

// inspect 执行一次巡检（PRD 3.4.1）
func (g *GuardianService) inspect() {
	// 防重叠：上一轮未完成则跳过本轮
	g.execMu.Lock()
	if g.executing {
		g.execMu.Unlock()
		logger.Warn("上一轮巡检未完成，跳过本轮")
		return
	}
	g.executing = true
	g.execMu.Unlock()

	defer func() {
		g.execMu.Lock()
		g.executing = false
		g.execMu.Unlock()
	}()

	// 加载守护配置（容错：读取/解析失败沿用上次有效配置）
	configs := g.loadConfigs()
	if len(configs) == 0 {
		return
	}

	// 获取本机所有网卡IP
	localIPs := g.getLocalIPs()

	// 本轮巡检统计
	var (
		totalCfg       = len(configs)
		skipIPMismatch int // IP不匹配跳过的组件数
		hcPass         int // 健康检查通过数
		hcFail         int // 健康检查失败数
		startSuccess   int // 启动脚本成功数
		startFail      int // 启动脚本失败数
	)

	// PRD 3.4.2：组件串行执行，按配置数组顺序依次守护
	for _, cfg := range configs {
		// Stop 提前返回，不再继续后续组件
		if g.isStopped() {
			break
		}
		result := g.handleComponent(cfg, localIPs)
		switch result {
		case componentSkipIPMismatch:
			skipIPMismatch++
		case componentHCPass:
			hcPass++
		case componentHCFail:
			hcFail++
		case componentStartSuccess:
			startSuccess++
		case componentStartFail:
			hcFail++
			startFail++
		}
	}

	// 打印本轮巡检汇总日志
	logger.Info("守护巡检本轮汇总",
		"配置总数", totalCfg,
		"IP跳过", skipIPMismatch,
		"健康检查通过", hcPass,
		"健康检查失败", hcFail,
		"启动脚本成功", startSuccess,
		"启动脚本失败", startFail,
	)
}

// componentHandleResult 组件守护处理结果枚举
type componentHandleResult int

const (
	componentSkipIPMismatch componentHandleResult = iota // IP 不匹配跳过
	componentHCPass                                      // 健康检查通过
	componentHCFail                                      // 健康检查失败（未自愈或无 startScript）
	componentStartSuccess                                // 健康检查失败但启动脚本成功（自愈成功）
	componentStartFail                                   // 健康检查失败且启动脚本失败（自愈失败）
)

// isStopped 查询服务是否已停止（非阻塞）
func (g *GuardianService) isStopped() bool {
	select {
	case <-g.stopCtx.Done():
		return true
	default:
		return false
	}
}

// loadConfigs 加载守护配置（PRD 3.4.1）
// BUG-5 修复：解析成功后做配置校验，过滤掉无效条目
func (g *GuardianService) loadConfigs() model.GuardianConfigList {
	data, err := os.ReadFile(g.configPath)
	if err != nil {
		if len(g.lastCfg) > 0 {
			logger.Warn("读取守护配置失败，沿用上次有效配置", "error", err)
			return g.lastCfg
		}
		logger.Error("读取守护配置失败", "error", err)
		return nil
	}

	var rawConfigs model.GuardianConfigList
	if err := yaml.Unmarshal(data, &rawConfigs); err != nil {
		if len(g.lastCfg) > 0 {
			logger.Warn("解析守护配置失败，沿用上次有效配置", "error", err)
			return g.lastCfg
		}
		logger.Error("解析守护配置失败", "error", err)
		return nil
	}

	// BUG-3 + BUG-5 修复：校验必填字段，过滤无效条目
	validated := validateConfigs(rawConfigs)
	if len(validated) == 0 && len(g.lastCfg) > 0 {
		logger.Warn("本轮配置全部校验不通过，沿用上次有效配置")
		return g.lastCfg
	}

	g.lastCfg = validated
	return validated
}

// validateConfigs 校验守护配置条目（BUG-3 + BUG-5）
// PRD 3.4.1 必填字段：componentName、ips、healthCheckScript
// 校验失败的条目打印 Warn 并从返回结果中移除，不影响其他有效条目
// 额外规则：自动排除 ips 中的 127.0.0.1 回环地址，若过滤后 ips 为空则该配置无效
func validateConfigs(configs model.GuardianConfigList) model.GuardianConfigList {
	var result model.GuardianConfigList
	for i, cfg := range configs {
		hasErr := false
		// componentName 必填
		if strings.TrimSpace(cfg.ComponentName) == "" {
			logger.Warn("守护配置校验失败：componentName 为空，跳过", "index", i)
			hasErr = true
		}

		// 过滤 ips 中的回环地址（127.0.0.1 / ::1）
		// 用户需求：不允许配置回环地址守护自身
		filteredIPs := make([]string, 0, len(cfg.IPs))
		hasLoopback := false
		for _, ip := range cfg.IPs {
			trimmedIP := strings.TrimSpace(ip)
			if trimmedIP == "127.0.0.1" || trimmedIP == "::1" {
				hasLoopback = true
				continue
			}
			filteredIPs = append(filteredIPs, trimmedIP)
		}
		if hasLoopback {
			logger.Warn("守护配置过滤：ips 中的回环地址(127.0.0.1/::1)已自动排除", "index", i, "component", cfg.ComponentName)
			cfg.IPs = filteredIPs
		}

		// ips 必填且至少 1 个
		if len(cfg.IPs) == 0 {
			logger.Warn("守护配置校验失败：ips 为空数组（或过滤 127.0.0.1 后为空），跳过", "index", i, "component", cfg.ComponentName)
			hasErr = true
		}

		// healthCheckScript 必填
		if strings.TrimSpace(cfg.HealthCheckScript) == "" {
			logger.Warn("守护配置校验失败：healthCheckScript 为空，跳过", "index", i, "component", cfg.ComponentName)
			hasErr = true
		}
		if !hasErr {
			result = append(result, cfg)
		}
	}
	return result
}

// getLocalIPs 获取本机所有网卡IP（仅统计处于UP状态的网卡，排除链路本地地址）
// 注意：回环地址（127.0.0.1 / ::1）仍保留在本机IP列表中，但配置端已禁止使用 127.0.0.1
func (g *GuardianService) getLocalIPs() []string {
	var ips []string
	interfaces, err := net.Interfaces()
	if err != nil {
		logger.Error("获取网卡信息失败", "error", err)
		return nil
	}

	for _, iface := range interfaces {
		// 跳过未启用的网卡
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipNet.IP
			// 跳过链路本地地址（fe80::/10 / 169.254.0.0/16），这类地址无路由意义
			if ip.IsLinkLocalUnicast() {
				continue
			}
			ips = append(ips, ip.String())
		}
	}

	return ips
}

// handleComponent 处理单个组件的守护逻辑（PRD 3.4.2）
// 返回 componentHandleResult 用于 inspect 汇总统计
func (g *GuardianService) handleComponent(cfg model.GuardianConfig, localIPs []string) componentHandleResult {
	// IP匹配检查：宿主机任意IP命中 ips 数组任一才执行守护
	if !g.ipMatch(cfg.IPs, localIPs) {
		return componentSkipIPMismatch
	}

	// PRD 3.4.2：健康检查，退出码 0 判定存活
	if g.performHealthCheck(cfg) {
		return componentHCPass
	}

	// 组件异常，执行自愈
	if cfg.StartScript == "" {
		logger.Error("组件异常但未配置 startScript", "component", cfg.ComponentName)
		return componentHCFail
	}

	// 执行启动脚本，返回启动脚本本身是否成功
	if g.performSelfHealing(cfg) {
		return componentStartSuccess
	}
	return componentStartFail
}

// ipMatch 检查本机IP是否与组件配置的IP列表匹配
func (g *GuardianService) ipMatch(configIPs []string, localIPs []string) bool {
	for _, cfgIP := range configIPs {
		for _, localIP := range localIPs {
			if cfgIP == localIP {
				return true
			}
		}
	}
	return false
}

// performHealthCheck 执行健康检查脚本（PRD 3.4.2）
// BUG-2 修复：ctx 从 g.stopCtx 派生，Stop 时立即取消；ShellExecutor 会强制 kill 子进程
func (g *GuardianService) performHealthCheck(cfg model.GuardianConfig) bool {
	if strings.TrimSpace(cfg.HealthCheckScript) == "" {
		return true
	}

	ctx, cancel := context.WithTimeout(g.stopCtx, time.Duration(g.healthCheckTimeout)*time.Second)
	defer cancel()

	_, _, exitCode, err := g.se.Exec(ctx, cfg.HealthCheckScript)
	if err != nil {
		if g.isStopped() {
			logger.Info("健康检查被停止信号中断", "component", cfg.ComponentName)
		} else {
			logger.Error("健康检查执行异常", "component", cfg.ComponentName, "error", err)
		}
		return false
	}
	return exitCode == 0
}

// performSelfHealing 执行自愈逻辑（PRD 3.4.2）
// 流程：startScript 拉起组件 → 立即执行健康检查 → 检查失败则打印错误日志
// 返回值仅表示 startScript 本身是否成功（退出码 0 且无异常），拉起后健康检查只做日志不影响返回值
// BUG-2 修复：两处 Shell ctx 都从 g.stopCtx 派生，Stop 时立即中断
func (g *GuardianService) performSelfHealing(cfg model.GuardianConfig) bool {
	logger.Info("执行组件自愈", "component", cfg.ComponentName)

	ctx, cancel := context.WithTimeout(g.stopCtx, time.Duration(g.startScriptTimeout)*time.Second)
	defer cancel()

	stdout, stderr, exitCode, err := g.se.Exec(ctx, cfg.StartScript)
	if g.isStopped() {
		logger.Info("启动脚本被停止信号中断", "component", cfg.ComponentName)
		return false
	}
	if err != nil {
		logger.Error("启动脚本执行异常",
			"component", cfg.ComponentName,
			"error", err,
			"stdout", stdout,
			"stderr", stderr,
		)
		return false
	}
	if exitCode != 0 {
		logger.Error("启动脚本非零退出码",
			"component", cfg.ComponentName,
			"exitCode", exitCode,
			"stdout", stdout,
			"stderr", stderr,
		)
		return false
	}

	// PRD 3.4.2：startScript 成功后立即再次执行健康检查
	logger.Info("启动脚本执行成功，立即执行拉起后健康检查", "component", cfg.ComponentName)

	ctx2, cancel2 := context.WithTimeout(g.stopCtx, time.Duration(g.healthCheckTimeout)*time.Second)
	defer cancel2()
	_, _, hcExitCode, hcErr := g.se.Exec(ctx2, cfg.HealthCheckScript)

	if g.isStopped() {
		logger.Info("拉起后健康检查被停止信号中断", "component", cfg.ComponentName)
		return true
	}
	if hcErr != nil {
		logger.Error("拉起后健康检查执行异常", "component", cfg.ComponentName, "error", hcErr)
		return true
	}
	if hcExitCode == 0 {
		logger.Info("组件自愈成功", "component", cfg.ComponentName)
	} else {
		logger.Error("拉起后健康检查仍失败", "component", cfg.ComponentName, "exitCode", hcExitCode)
	}
	return true
}

// ============ 暂停/恢复控制方法 ============

// Pause 暂停巡检执行
// 仅设置 paused 标记，不销毁 guardianLoop 协程；恢复时 Resume 置回 false 即可立即生效
// 幂等：已暂停时仅记录日志，不重复操作
// 线程安全：running 检查与 paused 赋值在同一 mu 临界区内，避免 Stop() 并发介入
func (g *GuardianService) Pause() {
	g.mu.Lock()
	defer g.mu.Unlock()

	if !g.running {
		logger.Error("GuardianService 未启动，无法暂停")
		return
	}

	if g.paused.CompareAndSwap(false, true) {
		g.setLastAction("pause")
		logger.Info("进程守护巡检已暂停")
	} else {
		logger.Info("进程守护巡检已处于暂停状态，跳过")
	}
}

// Resume 恢复巡检执行
// 幂等：运行中时仅记录日志，不重复操作
func (g *GuardianService) Resume() {
	g.mu.Lock()
	defer g.mu.Unlock()

	if !g.running {
		logger.Error("GuardianService 未启动，无法恢复")
		return
	}

	if g.paused.CompareAndSwap(true, false) {
		g.setLastAction("resume")
		logger.Info("进程守护巡检已恢复")
	} else {
		logger.Info("进程守护巡检已处于运行状态，跳过")
	}
}

// IsPaused 查询当前是否暂停中
func (g *GuardianService) IsPaused() bool {
	return g.paused.Load()
}

// IsRunning 查询服务是否已启动
func (g *GuardianService) IsRunning() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.running
}

// Status 返回服务完整状态快照
// 用于 HTTP Handler 查询接口
func (g *GuardianService) Status() model.GuardianStatus {
	g.mu.Lock()
	running := g.running
	g.mu.Unlock()

	g.statusMu.Lock()
	lastAction := g.lastAction
	lastActionAt := g.lastActionAt
	g.statusMu.Unlock()

	return model.GuardianStatus{
		Running:      running,
		Paused:       g.paused.Load(),
		Interval:     g.interval,
		LastAction:   lastAction,
		LastActionAt: lastActionAt,
	}
}

// setLastAction 记录最近一次控制操作（线程安全）
func (g *GuardianService) setLastAction(action string) {
	g.statusMu.Lock()
	g.lastAction = action
	g.lastActionAt = time.Now().Unix()
	g.statusMu.Unlock()
}
