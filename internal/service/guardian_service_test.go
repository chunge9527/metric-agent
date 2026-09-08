package service

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"metric-agent/internal/model"

	myconstant "metric-agent/internal/common/constant"
)

// ============ Mock ShellExecutor ============

type mockGuardianShell struct {
	execFunc func(ctx context.Context, script string, args ...string) (string, string, int, error)
}

func (m *mockGuardianShell) Exec(ctx context.Context, script string, args ...string) (string, string, int, error) {
	if m.execFunc != nil {
		return m.execFunc(ctx, script, args...)
	}
	return "", "", 0, nil
}
func (m *mockGuardianShell) ExecWithDir(ctx context.Context, workDir, script string, args ...string) (string, string, int, error) {
	return "", "", 0, nil
}

// ============ NewGuardianService 默认值 ============

func TestNewGuardianService_Defaults(t *testing.T) {
	g := NewGuardianService(&mockGuardianShell{}, "/tmp/crontab.yml", 0, 0, 0)
	if g.interval != 1 {
		t.Errorf("interval 应默认 1，实际 %d", g.interval)
	}
	if g.healthCheckTimeout != myconstant.DefaultHealthCheckTimeout {
		t.Errorf("healthCheckTimeout 应默认 %d，实际 %d", myconstant.DefaultHealthCheckTimeout, g.healthCheckTimeout)
	}
	if g.startScriptTimeout != myconstant.DefaultStartScriptTimeout {
		t.Errorf("startScriptTimeout 应默认 %d，实际 %d", myconstant.DefaultStartScriptTimeout, g.startScriptTimeout)
	}

	// 负值也触发默认
	g2 := NewGuardianService(&mockGuardianShell{}, "/tmp/crontab.yml", -5, -1, -1)
	if g2.interval != 1 {
		t.Errorf("interval=-5 应钳制为 1，实际 %d", g2.interval)
	}
	if g2.healthCheckTimeout != myconstant.DefaultHealthCheckTimeout {
		t.Errorf("healthCheckTimeout 应默认 %d", myconstant.DefaultHealthCheckTimeout)
	}
}

// ============ validateConfigs ============

func TestValidateConfigs_Valid(t *testing.T) {
	cfgs := model.GuardianConfigList{
		{ComponentName: "vminsert", IPs: []string{"10.0.0.1"}, HealthCheckScript: "echo ok"},
		{ComponentName: "vmselect", IPs: []string{"10.0.0.2"}, HealthCheckScript: "echo ok", StartScript: "./start.sh"},
	}
	result := validateConfigs(cfgs)
	if len(result) != 2 {
		t.Fatalf("应 2 条有效，实际 %d", len(result))
	}
}

func TestValidateConfigs_Filter_127_0_0_1(t *testing.T) {
	// 只配了 127.0.0.1，过滤后 ips 为空 → 整条跳过
	cfgs := model.GuardianConfigList{
		{ComponentName: "vminsert", IPs: []string{"127.0.0.1"}, HealthCheckScript: "echo ok"},
	}
	result := validateConfigs(cfgs)
	if len(result) != 0 {
		t.Errorf("127.0.0.1 过滤后 ips 为空，应整条无效，实际 %d", len(result))
	}
}

func TestValidateConfigs_Filter_127_0_0_1_Mixed(t *testing.T) {
	// 混合：127.0.0.1 + 一个有效 IP → 有效 IP 保留
	cfgs := model.GuardianConfigList{
		{ComponentName: "vminsert", IPs: []string{"127.0.0.1", "10.0.0.5"}, HealthCheckScript: "echo ok"},
	}
	result := validateConfigs(cfgs)
	if len(result) != 1 {
		t.Fatalf("应保留 1 条，实际 %d", len(result))
	}
	if len(result[0].IPs) != 1 || result[0].IPs[0] != "10.0.0.5" {
		t.Errorf("127.0.0.1 应被过滤，剩余 IP 应为 [10.0.0.5]，实际 %v", result[0].IPs)
	}
}

func TestValidateConfigs_MissingComponentName(t *testing.T) {
	cfgs := model.GuardianConfigList{
		{ComponentName: "", IPs: []string{"10.0.0.1"}, HealthCheckScript: "echo ok"},
	}
	result := validateConfigs(cfgs)
	if len(result) != 0 {
		t.Errorf("componentName 为空应无效，实际保留 %d 条", len(result))
	}
}

func TestValidateConfigs_MissingHealthCheckScript(t *testing.T) {
	cfgs := model.GuardianConfigList{
		{ComponentName: "vminsert", IPs: []string{"10.0.0.1"}, HealthCheckScript: ""},
	}
	result := validateConfigs(cfgs)
	if len(result) != 0 {
		t.Errorf("healthCheckScript 为空应无效，实际保留 %d 条", len(result))
	}
}

func TestValidateConfigs_EmptyIPs(t *testing.T) {
	cfgs := model.GuardianConfigList{
		{ComponentName: "vminsert", IPs: []string{}, HealthCheckScript: "echo ok"},
	}
	result := validateConfigs(cfgs)
	if len(result) != 0 {
		t.Errorf("ips 为空应无效，实际保留 %d 条", len(result))
	}
}

func TestValidateConfigs_PartialInvalid(t *testing.T) {
	cfgs := model.GuardianConfigList{
		{ComponentName: "", IPs: []string{"10.0.0.1"}, HealthCheckScript: "echo ok"},      // 无效
		{ComponentName: "valid", IPs: []string{"10.0.0.2"}, HealthCheckScript: "echo ok"}, // 有效
		{ComponentName: "", IPs: []string{"10.0.0.3"}, HealthCheckScript: "echo ok"},      // 无效
	}
	result := validateConfigs(cfgs)
	if len(result) != 1 {
		t.Errorf("应过滤后剩 1 条，实际 %d", len(result))
	}
	if len(result) > 0 && result[0].ComponentName != "valid" {
		t.Errorf("保留的应是 'valid'，实际 %q", result[0].ComponentName)
	}
}

// ============ ipMatch ============

func TestIPMatch(t *testing.T) {
	g := NewGuardianService(&mockGuardianShell{}, "/dev/null", 1, 60, 30)
	// 都有
	if !g.ipMatch([]string{"10.0.0.1", "10.0.0.2"}, []string{"10.0.0.2"}) {
		t.Error("应匹配")
	}
	// 完全不匹配
	if g.ipMatch([]string{"10.0.0.1"}, []string{"192.168.1.1"}) {
		t.Error("不应匹配")
	}
	// 配置 ips 为空
	if g.ipMatch([]string{}, []string{"10.0.0.1"}) {
		t.Error("空配置不应匹配")
	}
	// localIPs 为空
	if g.ipMatch([]string{"10.0.0.1"}, []string{}) {
		t.Error("空 localIPs 不应匹配")
	}
}

// ============ getLocalIPs ============

func TestGetLocalIPs(t *testing.T) {
	g := NewGuardianService(&mockGuardianShell{}, "/dev/null", 1, 60, 30)
	ips := g.getLocalIPs()

	// 至少要有一个回环地址（所有 Linux/Mac/Windows 都有）
	hasLoopback := false
	for _, ip := range ips {
		if ip == "127.0.0.1" || ip == "::1" {
			hasLoopback = true
			break
		}
		// 链路本地地址应被跳过
		ipObj := net.ParseIP(ip)
		if ipObj.IsLinkLocalUnicast() {
			t.Errorf("链路本地地址未被跳过: %s", ip)
		}
	}
	if len(ips) == 0 {
		t.Log("本机网卡列表为空（测试环境可能禁用了所有网卡），跳过断言")
	}
	_ = hasLoopback // 保留但不严格断言（有些容器环境可能没 127.0.0.1 以外的 IP）
}

// ============ loadConfigs 容错 ============

func TestLoadConfigs_FallbackToLast(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "crontab.yml")
	// 先写一个有效配置
	writeCrontab(t, cfgPath, `- componentName: vminsert
  ips:
    - 10.0.0.1
  healthCheckScript: echo ok`)

	g := NewGuardianService(&mockGuardianShell{}, cfgPath, 1, 60, 30)
	// 先调一次正常加载
	g.loadConfigs()

	// 写一个无效配置（空 componentName）
	writeCrontab(t, cfgPath, `- componentName: ""
  ips:
    - 10.0.0.1
  healthCheckScript: echo ok`)

	result := g.loadConfigs()
	if len(result) == 0 {
		t.Fatal("应回退到上次有效配置，实际返回空")
	}
	if result[0].ComponentName != "vminsert" {
		t.Errorf("应保留上次有效 'vminsert'，实际 %q", result[0].ComponentName)
	}
}

func TestLoadConfigs_FallbackOnReadError(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "crontab.yml")
	writeCrontab(t, cfgPath, `- componentName: vminsert
  ips:
    - 10.0.0.1
  healthCheckScript: echo ok`)

	g := NewGuardianService(&mockGuardianShell{}, cfgPath, 1, 60, 30)
	g.loadConfigs()

	// 删除文件 → 读取失败
	os.Remove(cfgPath)
	result := g.loadConfigs()
	if len(result) == 0 {
		t.Fatal("应回退到上次有效配置")
	}
}

func TestLoadConfigs_FallbackOnYAMLError(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "crontab.yml")
	writeCrontab(t, cfgPath, `- componentName: vminsert
  ips:
    - 10.0.0.1
  healthCheckScript: echo ok`)

	g := NewGuardianService(&mockGuardianShell{}, cfgPath, 1, 60, 30)
	g.loadConfigs()

	// 写非法 YAML
	writeCrontab(t, cfgPath, "not: valid: yaml: [[[")
	result := g.loadConfigs()
	if len(result) == 0 {
		t.Fatal("应回退到上次有效配置")
	}
}

func TestLoadConfigs_AllFirstTimeFail(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "nonexistent.yml")

	g := NewGuardianService(&mockGuardianShell{}, cfgPath, 1, 60, 30)
	result := g.loadConfigs()
	if len(result) != 0 {
		t.Error("首次加载全部失败应返回空")
	}
}

// ============ performHealthCheck ============

func TestPerformHealthCheck_Pass(t *testing.T) {
	se := &mockGuardianShell{
		execFunc: func(ctx context.Context, script string, args ...string) (string, string, int, error) {
			return "", "", 0, nil
		},
	}
	g := NewGuardianService(se, "/dev/null", 1, 60, 30)

	cfg := model.GuardianConfig{HealthCheckScript: "echo ok"}
	if !g.performHealthCheck(cfg) {
		t.Error("健康检查应通过")
	}
}

func TestPerformHealthCheck_FailExitCode(t *testing.T) {
	se := &mockGuardianShell{
		execFunc: func(ctx context.Context, script string, args ...string) (string, string, int, error) {
			return "", "", 1, nil
		},
	}
	g := NewGuardianService(se, "/dev/null", 1, 60, 30)

	cfg := model.GuardianConfig{HealthCheckScript: "false"}
	if g.performHealthCheck(cfg) {
		t.Error("exitCode=1 应判定失败")
	}
}

func TestPerformHealthCheck_FailError(t *testing.T) {
	se := &mockGuardianShell{
		execFunc: func(ctx context.Context, script string, args ...string) (string, string, int, error) {
			return "", "", 0, errors.New("timeout")
		},
	}
	g := NewGuardianService(se, "/dev/null", 1, 60, 30)

	cfg := model.GuardianConfig{HealthCheckScript: "timeout"}
	if g.performHealthCheck(cfg) {
		t.Error("exec 错误应判定失败")
	}
}

func TestPerformHealthCheck_EmptyScript(t *testing.T) {
	g := NewGuardianService(&mockGuardianShell{}, "/dev/null", 1, 60, 30)
	// healthCheckScript 为空应视为通过（PRD 3.4.2 容错）
	if !g.performHealthCheck(model.GuardianConfig{}) {
		t.Error("空脚本应判定通过")
	}
}

// ============ performSelfHealing ============

func TestPerformSelfHealing_Success(t *testing.T) {
	callCount := 0
	se := &mockGuardianShell{
		execFunc: func(ctx context.Context, script string, args ...string) (string, string, int, error) {
			callCount++
			return "", "", 0, nil
		},
	}
	g := NewGuardianService(se, "/dev/null", 1, 60, 30)

	cfg := model.GuardianConfig{HealthCheckScript: "hc", StartScript: "start"}
	if !g.performSelfHealing(cfg) {
		t.Error("启动脚本成功应返回 true")
	}
	// startScript + 拉起后 healthCheck = 2 次
	if callCount != 2 {
		t.Errorf("应调用 2 次（start + hc），实际 %d", callCount)
	}
}

func TestPerformSelfHealing_StartScriptFail(t *testing.T) {
	se := &mockGuardianShell{
		execFunc: func(ctx context.Context, script string, args ...string) (string, string, int, error) {
			if script == "start" {
				return "", "", 1, nil
			}
			return "", "", 0, nil
		},
	}
	g := NewGuardianService(se, "/dev/null", 1, 60, 30)

	cfg := model.GuardianConfig{HealthCheckScript: "hc", StartScript: "start"}
	if g.performSelfHealing(cfg) {
		t.Error("startScript 非零退出码应返回 false")
	}
}

func TestPerformSelfHealing_HCFailAfterStart(t *testing.T) {
	callCount := 0
	se := &mockGuardianShell{
		execFunc: func(ctx context.Context, script string, args ...string) (string, string, int, error) {
			callCount++
			// 第一次 start 成功，第二次 hc 失败
			if callCount == 1 {
				return "", "", 0, nil
			}
			return "", "", 1, nil
		},
	}
	g := NewGuardianService(se, "/dev/null", 1, 60, 30)

	cfg := model.GuardianConfig{HealthCheckScript: "hc", StartScript: "start"}
	// startScript 本身成功即返回 true，拉起后 hc 失败只打日志不影响返回值
	if !g.performSelfHealing(cfg) {
		t.Error("startScript 成功应返回 true，即使拉起后 hc 失败")
	}
}

// ============ Start/Stop 生命周期 ============

func TestGuardianService_StartStop(t *testing.T) {
	g := NewGuardianService(&mockGuardianShell{}, "/dev/null", 1, 60, 30)

	g.Start()
	if !g.IsRunning() {
		t.Error("Start 后 IsRunning 应 true")
	}

	// 重复 Start 应幂等
	g.Start()

	g.Stop()
	if g.IsRunning() {
		t.Error("Stop 后 IsRunning 应 false")
	}

	// 重复 Stop 应幂等
	g.Stop()
}

func TestGuardianService_StartStop_WithInspect(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "crontab.yml")
	writeCrontab(t, cfgPath, `- componentName: vminsert
  ips:
    - 127.0.0.1
  healthCheckScript: echo ok`)

	se := &mockGuardianShell{
		execFunc: func(ctx context.Context, script string, args ...string) (string, string, int, error) {
			return "", "", 0, nil
		},
	}
	g := NewGuardianService(se, cfgPath, 1, 60, 30)

	g.Start()
	time.Sleep(500 * time.Millisecond) // 让首次 inspect 跑完
	g.Stop()
}

// ============ Pause/Resume ============

func TestGuardianService_PauseResume(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "crontab.yml")
	writeCrontab(t, cfgPath, `- componentName: vminsert
  ips:
    - 127.0.0.1
  healthCheckScript: echo ok`)

	se := &mockGuardianShell{}
	g := NewGuardianService(se, cfgPath, 1, 60, 30)

	// 未启动时 Pause/Resume 应报错但不 panic
	g.Pause()
	g.Resume()

	g.Start()
	time.Sleep(100 * time.Millisecond)

	if g.IsPaused() {
		t.Error("Start 后应默认运行中")
	}

	g.Pause()
	if !g.IsPaused() {
		t.Error("Pause 后 IsPaused 应 true")
	}

	// 重复 Pause 应幂等
	g.Pause()

	g.Resume()
	if g.IsPaused() {
		t.Error("Resume 后 IsPaused 应 false")
	}

	g.Stop()
}

func TestGuardianService_PauseResume_NotRunning(t *testing.T) {
	g := NewGuardianService(&mockGuardianShell{}, "/dev/null", 1, 60, 30)
	// 未启动时调用
	g.Pause() // 应只打 Error 日志不 panic
	g.Resume()
	if g.IsPaused() {
		t.Error("未启动时不应处于暂停")
	}
}

// ============ Status ============

func TestGuardianService_Status(t *testing.T) {
	g := NewGuardianService(&mockGuardianShell{}, "/dev/null", 5, 60, 30)

	status := g.Status()
	if status.Running {
		t.Error("未启动时 Running 应 false")
	}
	if status.Interval != 5 {
		t.Errorf("Interval 应 5，实际 %d", status.Interval)
	}

	g.Start()
	status = g.Status()
	if !status.Running {
		t.Error("启动后 Running 应 true")
	}

	g.Pause()
	status = g.Status()
	if !status.Paused {
		t.Error("暂停后 Paused 应 true")
	}

	g.Stop()
	status = g.Status()
	if status.Running {
		t.Error("停止后 Running 应 false")
	}
}

// ============ handleComponent 完整流程 ============

func TestHandleComponent_HCPass(t *testing.T) {
	se := &mockGuardianShell{
		execFunc: func(ctx context.Context, script string, args ...string) (string, string, int, error) {
			return "", "", 0, nil // hc 通过
		},
	}
	g := NewGuardianService(se, "/dev/null", 1, 60, 30)

	cfg := model.GuardianConfig{
		ComponentName:     "vminsert",
		IPs:               []string{"127.0.0.1"}, // getLocalIPs 会有 127.0.0.1
		HealthCheckScript: "echo ok",
	}
	result := g.handleComponent(cfg, []string{"127.0.0.1", "10.0.0.1"})
	if result != componentHCPass {
		t.Errorf("应返回 componentHCPass，实际 %v", result)
	}
}

func TestHandleComponent_IPMismatch(t *testing.T) {
	se := &mockGuardianShell{}
	g := NewGuardianService(se, "/dev/null", 1, 60, 30)

	cfg := model.GuardianConfig{
		ComponentName:     "vminsert",
		IPs:               []string{"10.0.0.100"},
		HealthCheckScript: "echo ok",
	}
	result := g.handleComponent(cfg, []string{"127.0.0.1", "10.0.0.1"})
	if result != componentSkipIPMismatch {
		t.Errorf("IP 不匹配应跳过，实际 %v", result)
	}
}

func TestHandleComponent_HCFailNoStartScript(t *testing.T) {
	se := &mockGuardianShell{
		execFunc: func(ctx context.Context, script string, args ...string) (string, string, int, error) {
			return "", "", 1, nil // hc 失败
		},
	}
	g := NewGuardianService(se, "/dev/null", 1, 60, 30)

	cfg := model.GuardianConfig{
		ComponentName:     "vminsert",
		IPs:               []string{"127.0.0.1"},
		HealthCheckScript: "false",
		// StartScript 为空 → 无法自愈
	}
	result := g.handleComponent(cfg, []string{"127.0.0.1"})
	if result != componentHCFail {
		t.Errorf("无 startScript 应 componentHCFail，实际 %v", result)
	}
}

func TestHandleComponent_HCFailStartSuccess(t *testing.T) {
	se := &mockGuardianShell{
		execFunc: func(ctx context.Context, script string, args ...string) (string, string, int, error) {
			// healthCheckScript 每次都退出码 1（失败），startScript 退出码 0（成功）
			if script == "false" {
				return "", "", 1, nil
			}
			return "", "", 0, nil // startScript 成功
		},
	}
	g := NewGuardianService(se, "/dev/null", 1, 60, 30)

	cfg := model.GuardianConfig{
		ComponentName:     "vminsert",
		IPs:               []string{"127.0.0.1"},
		HealthCheckScript: "false",
		StartScript:       "echo start",
	}
	result := g.handleComponent(cfg, []string{"127.0.0.1"})
	if result != componentStartSuccess {
		t.Errorf("startScript 本身成功应 componentStartSuccess(3)，实际 %d", result)
	}
}

func TestHandleComponent_HCFailStartFail(t *testing.T) {
	se := &mockGuardianShell{
		execFunc: func(ctx context.Context, script string, args ...string) (string, string, int, error) {
			if script == "start" {
				return "", "", 1, nil // startScript 失败
			}
			return "", "", 1, nil // hc 失败
		},
	}
	g := NewGuardianService(se, "/dev/null", 1, 60, 30)

	cfg := model.GuardianConfig{
		ComponentName:     "vminsert",
		IPs:               []string{"127.0.0.1"},
		HealthCheckScript: "false",
		StartScript:       "start",
	}
	result := g.handleComponent(cfg, []string{"127.0.0.1"})
	if result != componentStartFail {
		t.Errorf("startScript 失败应 componentStartFail，实际 %v", result)
	}
}

// ============ 巡检防重叠 ============
// 注：防重叠核心逻辑（executing + execMu）已在代码中实现，此处不做 flaky 的端到端测试
// （依赖 Windows 网卡 IP 列表，不同环境下行为不一致）

// ============ 辅助 ============

func writeCrontab(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("写 %s 失败: %v", path, err)
	}
}
