// Package main MetricAgent主入口
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	"metric-agent/internal/bootstrap"
	myconstant "metric-agent/internal/common/constant"
	"metric-agent/internal/common/logger"
	"metric-agent/internal/infra/crypto"
	"metric-agent/internal/model"
)

// httpFlagSet HTTP模式参数集合（包级 var，避免每次 detectExplicitHTTPFlags 重建 map）
var httpFlagSet = map[string]string{
	"-config":     "--config",
	"--config":    "--config",
	"-bind-addr":  "--bind-addr",
	"--bind-addr": "--bind-addr",
	"-logs":       "--logs",
	"--logs":      "--logs",
}

// execFlagSet 指令模式参数集合（用于 detectExplicitExecFlags）
var execFlagSet = map[string]string{
	"-exec":       "--exec",
	"--exec":      "--exec",
	"-exec-file":  "--exec-file",
	"--exec-file": "--exec-file",
	"-port":       "--port",
	"--port":      "--port",
	"-timeout":    "--timeout",
	"--timeout":   "--timeout",
}

func main() {
	// ========== 命令行参数解析 ==========
	var (
		helpFlag    bool
		execFlag    string
		execFile    string
		execPort    int
		execTimeout int
		configFlag  string
		bindAddr    string
		logsFlag    string
	)

	// 自定义Usage，统一错误提示格式
	flag.Usage = func() {
		printUsage()
	}

	flag.BoolVar(&helpFlag, "help", false, "显示帮助信息")
	flag.StringVar(&execFlag, "exec", "", "指令执行模式：Shell脚本字符串")
	flag.StringVar(&execFile, "exec-file", "", "指令执行模式：Shell脚本文件路径")
	flag.IntVar(&execPort, "port", 9092, "指令执行模式：目标HTTP服务端口（发送请求到 127.0.0.1:port）")
	flag.IntVar(&execTimeout, "timeout", 0, "指令执行模式：执行超时秒数（默认使用服务端 DefaultExecTimeout=60）")
	flag.StringVar(&configFlag, "config", myconstant.DefaultConfigPath, "HTTP服务模式：配置文件路径（相对路径基于可执行文件目录）")
	flag.StringVar(&bindAddr, "bind-addr", myconstant.DefaultBindAddr, "HTTP服务模式：监听地址")
	flag.StringVar(&logsFlag, "logs", "", "HTTP服务模式：日志文件路径（相对路径基于可执行文件目录，优先级高于YAML中log.logFile）")

	flag.Parse()

	// ========== 帮助信息（最高优先级） ==========
	if helpFlag {
		printUsage()
		os.Exit(0)
	}

	// ========== --exec 与 --exec-file 互斥校验 ==========
	if execFlag != "" && execFile != "" {
		fmt.Fprintln(os.Stderr, "错误：--exec 和 --exec-file 不能同时使用")
		printUsage()
		os.Exit(1)
	}

	// ========== 模式判定 ==========
	isExecMode := execFlag != "" || execFile != ""

	// ========== 模式互斥校验（双向检测） ==========
	// 检测1：指令模式里混入 HTTP 参数（原逻辑）
	explicitHTTPFlags := detectExplicitHTTPFlags(os.Args)
	if isExecMode && len(explicitHTTPFlags) > 0 {
		fmt.Fprintf(os.Stderr, "错误：指令执行模式与HTTP服务模式参数(%s)互斥\n", strings.Join(explicitHTTPFlags, ", "))
		printUsage()
		os.Exit(1)
	}
	// 检测2：HTTP 模式里混入指令参数（新增）
	explicitExecFlags := detectExplicitExecFlags(os.Args)
	if !isExecMode && len(explicitExecFlags) > 0 {
		fmt.Fprintf(os.Stderr, "错误：HTTP服务模式与指令执行模式参数(%s)互斥\n", strings.Join(explicitExecFlags, ", "))
		printUsage()
		os.Exit(1)
	}

	// ========== 获取 -- 后的参数（保留原始数组） ==========
	// P0-1：直接传 []string 给 runExecMode，由 ShellExecutor 用 shell -c "script" args... 传递
	// 保留含空格/特殊字符参数的原始 shell 语义
	args := flag.Args()

	// ========== 指令执行模式 ==========
	if isExecMode {
		os.Exit(runExecMode(execFlag, execFile, args, execPort, execTimeout))
	}

	// ========== HTTP服务模式 ==========
	os.Exit(runHTTPMode(configFlag, bindAddr, logsFlag))
}

// detectExplicitHTTPFlags 检测命令行中是否显式传入了HTTP模式参数
// Go flag 同时接受单横线（-config）和双横线（--config）形式，两种都需识别
func detectExplicitHTTPFlags(args []string) []string {
	var found []string
	for _, arg := range args {
		argName := arg
		if idx := strings.Index(arg, "="); idx > 0 {
			argName = arg[:idx]
		}
		if name, ok := httpFlagSet[argName]; ok {
			found = append(found, name)
		}
	}
	return found
}

// detectExplicitExecFlags 检测命令行中是否显式传入了指令模式参数（--exec/--exec-file/--port/--timeout）
// 用于 HTTP 模式下拦截混入的指令参数
func detectExplicitExecFlags(args []string) []string {
	var found []string
	for _, arg := range args {
		argName := arg
		if idx := strings.Index(arg, "="); idx > 0 {
			argName = arg[:idx]
		}
		if name, ok := execFlagSet[argName]; ok {
			found = append(found, name)
		}
	}
	return found
}

// runExecMode 指令执行模式
// 不加载任何配置文件，不启动 HTTP 服务，不初始化日志；
// 组装 HTTP POST 请求，向已运行的本地 MetricAgent 服务发 AES 加密请求；
// 执行完成后根据响应中的 exit_code 直接退出
func runExecMode(execScript string, execFile string, args []string, port int, timeoutSec int) int {
	// ========== 客户端参数校验 ==========
	if port <= 0 || port > 65535 {
		fmt.Fprintf(os.Stderr, "错误：--port 参数值 %d 不合法，有效范围 1-65535\n", port)
		return 1
	}
	if timeoutSec < 0 {
		fmt.Fprintf(os.Stderr, "错误：--timeout 参数值 %d 不合法，不能为负数\n", timeoutSec)
		return 1
	}
	if timeoutSec > myconstant.ClientMaxExecTimeout {
		fmt.Fprintf(os.Stderr, "错误：--timeout 参数值 %d 超出客户端上限 %d 秒\n", timeoutSec, myconstant.ClientMaxExecTimeout)
		return 1
	}

	// ========== 初始化 AES 加密器 ==========
	// PRD 2.2：指令执行模式不加载配置文件，使用硬编码密钥常量
	aesCrypto, err := crypto.NewAESCrypto(myconstant.DefaultExecModeAESKey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：AES加密器初始化失败 %v\n", err)
		return 1
	}

	// ========== 解析脚本内容 ==========
	var script string
	if execScript != "" {
		script = execScript
	} else if execFile != "" {
		resolvedPath := bootstrap.BootstrapPath(execFile)
		data, err := os.ReadFile(resolvedPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "错误：读取脚本文件失败 %v\n", err)
			return 1
		}
		script = string(data)
	} else {
		fmt.Fprintln(os.Stderr, "错误：未提供脚本内容（--exec 或 --exec-file）")
		return 1
	}

	// ========== 组装 HTTP 请求 ==========
	// 1. 构造 ExecRequest JSON（script + args 数组分开传递，保留 shell 参数语义）
	reqObj := map[string]any{
		"script": script,
	}
	if len(args) > 0 {
		reqObj["args"] = args
	}
	if timeoutSec > 0 {
		reqObj["timeout"] = timeoutSec
	}
	reqJSON, err := json.Marshal(reqObj)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：请求序列化失败 %v\n", err)
		return 1
	}

	// 2. AES 加密请求体
	reqBody, err := aesCrypto.Encrypt(reqJSON)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：请求体AES加密失败 %v\n", err)
		return 1
	}

	// 3. 构造目标 URL
	url := fmt.Sprintf("http://127.0.0.1:%d%s", port, myconstant.RouteExec)

	// ========== 发送 HTTP 请求 ==========
	// 仅用 context 控制超时
	// context.WithTimeout 覆盖请求全链路（TCP握手 → 请求发送 → 响应等待 → 响应体读取）
	ctxTimeout := myconstant.DefaultExecTimeout + 5
	if timeoutSec > 0 {
		ctxTimeout = timeoutSec + 5
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(ctxTimeout)*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：构造请求失败 %v\n", err)
		return 1
	}
	req.Header.Set("Content-Type", "application/octet-stream")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：无法连接到本地 MetricAgent 服务 (127.0.0.1:%d)：%v\n", port, err)
		fmt.Fprintln(os.Stderr, "请先启动 MetricAgent HTTP 服务或确认端口后再执行指令模式")
		return 1
	}
	defer resp.Body.Close()

	// ========== 解析响应 ==========
	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：读取响应失败 %v\n", err)
		return 1
	}

	// 通过 Content-Type 判断是否为 AES 加密响应：
	//   application/octet-stream → ExecHandler 的 AES 加密响应（成功或失败）
	//   application/json 等    → 非 ExecHandler 响应（如 auth_middleware 的 401、router 的 404）
	contentType := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Type")))
	var plaintext []byte
	if strings.HasPrefix(contentType, "application/octet-stream") {
		// AES 加密响应（ExecHandler 路径）
		plaintext, err = aesCrypto.Decrypt(respBytes)
		if err != nil {
			fmt.Fprintf(os.Stderr, "错误：响应体AES解密失败 %v, body_len=%d\n", err, len(respBytes))
			return 1
		}
	} else {
		// 非 ExecHandler 响应：明文 JSON
		plaintext = respBytes
	}

	if resp.StatusCode != http.StatusOK {
		var errResp model.ErrorResponse
		if json.Unmarshal(plaintext, &errResp) == nil && errResp.Code != 0 {
			fmt.Fprintf(os.Stderr, "错误（HTTP %d）：%s\n", errResp.Code, errResp.Message)
		} else {
			fmt.Fprintf(os.Stderr, "错误：HTTP %d, body=%s\n", resp.StatusCode, string(plaintext))
		}
		return 1
	}

	// 成功：解析 ExecResponse
	var execResp model.ExecResponse
	if err := json.Unmarshal(plaintext, &execResp); err != nil {
		fmt.Fprintf(os.Stderr, "错误：解析执行响应失败 %v, body=%s\n", err, string(plaintext))
		return 1
	}

	// ========== 输出结果 ==========
	if execResp.Stdout != "" {
		fmt.Print(execResp.Stdout)
	}
	if execResp.Stderr != "" {
		fmt.Fprint(os.Stderr, execResp.Stderr)
	}

	// exitCode < 0 表示 ShellExecutor 内部异常（如启动失败），返回非 0
	if execResp.ExitCode < 0 {
		return 1
	}
	return execResp.ExitCode
}

// runHTTPMode HTTP服务模式
func runHTTPMode(configPath string, bindAddr string, logPath string) int {
	// ========== 第一步：先加载 yaml 配置 ==========
	// logger.InitFileLogger 依赖 cfg.Log（级别、单文件大小、保留天数），
	// 必须在 logger 初始化之前完成加载
	cfg, err := bootstrap.LoadBaseConfig(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：加载配置失败 %v\n", err)
		return 1
	}

	// ========== 第二步：初始化日志 ==========
	// 日志文件路径三级优先级：
	//   1. 命令行 --logs 显式值（最优先，空字符串表示未显式传入）
	//   2. YAML log.logFile 配置值
	//   3. DefaultLogPath 内置默认值
	logFile := myconstant.DefaultLogPath
	switch {
	case strings.TrimSpace(logPath) != "":
		logFile = logPath
	case strings.TrimSpace(cfg.Log.LogFile) != "":
		logFile = cfg.Log.LogFile
	}
	resolvedLogPath := bootstrap.BootstrapPath(logFile)
	if err := logger.InitFileLogger(resolvedLogPath, cfg.Log); err != nil {
		fmt.Fprintf(os.Stderr, "错误：日志初始化失败 %v\n", err)
		return 1
	}

	logger.Info("MetricAgent启动中",
		"agent_id", cfg.Agent.ID,
		"agent_group", cfg.Agent.Group,
		"config", configPath,
		"bind_addr", bindAddr,
		"log_level", cfg.Log.Level,
		"log_file", resolvedLogPath,
	)

	// ============ 端口预探测（提前到 InitServer 之前） ============
	// InitServer 是重型操作（Nacos TCP连接探测、后台协程启动、路由注册等），
	// 端口探测仅需 bindAddr 字符串，完全不依赖其产物；
	// 竞态窗口（探测 Close → 真正 bind 之间）不变：InitServer 耗时是两者共同的窗口背景。
	if err := probeBind(bindAddr); err != nil {
		logger.Error("HTTP服务端口探测失败", "addr", bindAddr, "error", err)
		fmt.Fprintf(os.Stderr, "错误：端口 %s 已被占用或无法绑定\n", bindAddr)
		fmt.Fprintln(os.Stderr, "请更换 --bind-addr 端口或停止占用该端口的进程后重试")
		return 1 // InitServer 还没调用，无需 safeCleanup
	}

	// 初始化服务
	srv, cleanup, err := bootstrap.InitServer(cfg, bindAddr)
	if err != nil {
		logger.Error("服务初始化失败", "error", err)
		fmt.Fprintf(os.Stderr, "错误：服务初始化失败 %v\n", err)
		return 1
	}

	// 用 sync.Once 包装 cleanup，保证无论哪个路径触发（启动失败 / 运行期异常 / 信号关闭）都只执行一次
	// 避免 double cleanup 导致的二次关闭 panic 或竞态
	var cleanupOnce sync.Once
	safeCleanup := func() {
		cleanupOnce.Do(func() {
			cleanup()
			// 注意：logger.Close() 不在此处调用，由 WaitForShutdown 或错误路径在所有日志写完后调用
		})
	}

	// P3-1：重构 errCh 竞争问题
	// 原来 errCh 有两个 goroutine 监听（select 启动期 + 后台运行期），谁先抢到不确定
	// 重构为单 goroutine + typed channel 统一处理，语义清晰
	type serveError struct {
		stage string // "startup" 或 "runtime"
		err   error
	}
	errCh := make(chan serveError, 1)
	go func() {
		logger.Info("HTTP服务启动", "addr", bindAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- serveError{stage: "runtime", err: err}
		}
	}()

	// 给HTTP服务启动时间，检查是否立即失败（端口占用等）
	select {
	case serveErr := <-errCh:
		// HTTP服务启动失败，立即清理并退出
		logger.Error("HTTP服务启动失败", "error", serveErr.err)
		fmt.Fprintf(os.Stderr, "错误：HTTP服务启动失败 %v\n", serveErr.err)
		if isAddrInUseErr(serveErr.err) {
			fmt.Fprintf(os.Stderr, "端口 %s 已被占用，请更换端口或停止占用进程\n", bindAddr)
		}
		safeCleanup()
		logger.Close() // 最后关闭日志，确保 shutdown 阶段所有日志入文件后再关闭
		return 1
	case <-time.After(200 * time.Millisecond):
		// 启动成功（或在短时间内未失败），进入正常等待流程
	}

	// 单 goroutine 统一监听运行期异常
	go func() {
		if se := <-errCh; se.err != nil {
			logger.Error("HTTP服务运行异常，执行清理后退出", "stage", se.stage, "error", se.err)
			fmt.Fprintf(os.Stderr, "错误：HTTP服务运行异常 %v\n", se.err)
			safeCleanup()
			logger.Close() // 最后关闭日志，确保 shutdown 阶段所有日志入文件后再关闭
			os.Exit(1)
		}
	}()

	// 主线程阻塞等待信号触发优雅关闭
	// WaitForShutdown 内部：监听信号 → srv.Shutdown() → safeCleanup() → os.Exit(0)
	// 正常情况下不会返回；return 0 是为了满足 Go 编译器对有返回值函数的约束
	bootstrap.WaitForShutdown(srv, safeCleanup)

	return 0
}

// printUsage 打印帮助信息
func printUsage() {
	fmt.Println("MetricAgent - 轻量级监控节点运维代理")
	fmt.Println()
	fmt.Println("用法:")
	fmt.Println("  metric-agent [flags]")
	fmt.Println()
	fmt.Println("指令执行模式参数（一次性执行后退出）:")
	fmt.Println("  --exec string        Shell脚本字符串")
	fmt.Println("  --exec-file string   Shell脚本文件路径（相对路径基于可执行文件目录）")
	fmt.Println("  --port int           目标HTTP服务端口 (default 9092)")
	fmt.Println("  --timeout int        执行超时秒数 (default 60，上限 1800)")
	fmt.Println("  --                   分隔符，其后内容作为脚本参数传递（含空格参数不丢失语义）")
	fmt.Println()
	fmt.Println("HTTP服务模式参数（常驻后台）:")
	fmt.Println("  --config string      配置文件路径 (default \"./metricAgent.yml\")")
	fmt.Println("                       相对路径基于可执行文件所在目录，支持绝对路径")
	fmt.Println("  --bind-addr string   HTTP监听地址 (default \"0.0.0.0:9092\")")
	fmt.Println("  --logs string        日志文件路径 (YAML可配: log.logFile, 默认 \"./logs/metricAgent.log\")")
	fmt.Println("                       优先级: --logs > log.logFile > 默认值")
	fmt.Println("                       相对路径基于可执行文件所在目录")
	fmt.Println()
	fmt.Println("通用参数:")
	fmt.Println("  --help               显示帮助信息（最高优先级）")
	fmt.Println()
	fmt.Println("业务规则:")
	fmt.Println("  1. 指令模式与HTTP模式参数互斥，同时传入两类参数直接报错")
	fmt.Println("  2. --exec 与 --exec-file 互斥，只能使用其中一个")
	fmt.Println("  3. 指令模式向本地 MetricAgent HTTP 服务发 AES 加密请求，请求体和返回值均使用 AES 加解密传输")
	fmt.Println("  4. --help 与其他参数同时出现时仅输出帮助并退出")
	fmt.Println()
	fmt.Println("示例:")
	fmt.Println("  # 先启动 MetricAgent HTTP 服务")
	fmt.Println("  metric-agent --config ./metricAgent.yml")
	fmt.Println()
	fmt.Println("  # 指令模式 - 直接执行脚本字符串（向 127.0.0.1:9092 发请求）")
	fmt.Println("  metric-agent --exec 'echo \"hello world\"'")
	fmt.Println()
	fmt.Println("  # 指令模式 - 指定端口、执行脚本文件并传参（含空格参数正确传递）")
	fmt.Println("  metric-agent --exec-file ./script.sh --port 9092 --timeout 30 -- arg1 '/some/path with spaces' arg2")
}

// probeBind 尝试对给定地址做一次 TCP bind，返回 nil 表示端口可用
// 成功后立即 Close listener，仅用于启动前的快速可用性探测
func probeBind(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return ln.Close()
}

// isAddrInUseErr 判断错误是否为端口/地址被占用（跨平台）
// 用 errors.Is 匹配 syscall.EADDRINUSE（Go syscall 包跨平台暴露各自 OS 的地址占用错误码）
// 并兼容 *net.OpError 包装链的 unwrap
func isAddrInUseErr(err error) bool {
	if errors.Is(err, syscall.EADDRINUSE) {
		return true
	}
	// 兜底：检查 *net.OpError 内部的 Err 字段（某些 Go 版本或平台 errors.Is 可能没 unwrap 到底）
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		if opErr.Op == "listen" && errors.Is(opErr.Err, syscall.EADDRINUSE) {
			return true
		}
	}
	return false
}
