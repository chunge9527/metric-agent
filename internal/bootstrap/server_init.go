package bootstrap

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	myconstant "metric-agent/internal/common/constant"
	"metric-agent/internal/common/logger"
	"metric-agent/internal/infra/crypto"
	"metric-agent/internal/infra/iface"
	"metric-agent/internal/infra/nacos_client"
	"metric-agent/internal/infra/shell_exec"
	"metric-agent/internal/model"
	"metric-agent/internal/service"
)

// InitServer 初始化HTTP服务，返回HTTP Server、清理函数和错误
// 根据Feature开关选择性初始化Nacos / 进程守护 / 定时任务
func InitServer(cfg *model.AgentConfig, bindAddr string) (*http.Server, func(), error) {
	// ========== 打印Feature开关状态 ==========
	nacosOn := cfg.Feature.IsNacosEnabled()
	guardianOn := cfg.Feature.IsGuardianEnabled()
	scheduleOn := cfg.Feature.IsScheduleEnabled()
	logger.Info("服务特性开关",
		"enableNacos", nacosOn,
		"enableGuardian", guardianOn,
		"enableSchedule", scheduleOn,
	)

	// ========== 增量式清理注册 ==========
	var cleanupFuncs []func()
	cleanup := func() {
		logger.Info("执行服务清理...")
		// 逆序清理（后创建的先清理）
		for i := len(cleanupFuncs) - 1; i >= 0; i-- {
			func() {
				defer func() {
					if r := recover(); r != nil {
						logger.Error("清理函数panic已捕获", "error", fmt.Sprint(r))
					}
				}()
				cleanupFuncs[i]()
			}()
		}
		logger.Info("服务清理完成")
	}

	// ========== 基础设施层初始化 ==========

	// Nacos客户端（开关关闭则不创建，使用空实现避免typed-nil接口陷阱）
	var cc iface.ConfigCenter = iface.NoopConfigCenter()
	if nacosOn {
		// NewNacosClient 内部会做 TCP 连通性探测，失败返回 error；
		// 此处 warn 提示但不阻断启动，运行时 reconnectLoop 会按 NacosRuntimeRetryInterval 继续重试
		nacosClient, err := nacos_client.NewNacosClient(cfg.Nacos)
		if err != nil {
			logger.Warn("Nacos 连接初始化失败，将在运行时自动重试", "error", err)
		}
		cc = nacosClient
		cleanupFuncs = append(cleanupFuncs, func() {
			_ = cc.Close()
		})
	} else {
		logger.Info("Nacos配置拉取服务已禁用，跳过初始化")
	}

	// Shell执行器
	se := shell_exec.NewShellExecutor()

	// AES加密（直接判断err，避免typed-nil接口陷阱）
	if cfg.Shell.Encrypt.Key == "" {
		cleanup()
		return nil, nil, fmt.Errorf("AES加密密钥未配置（shell.encrypt.key）")
	}
	aesCrypto, aesErr := crypto.NewAESCrypto(cfg.Shell.Encrypt.Key)
	if aesErr != nil {
		logger.Error("AES加密初始化失败", "error", aesErr)
		cleanup()
		return nil, nil, fmt.Errorf("AES加密初始化失败: %w", aesErr)
	}
	var cryptoImpl iface.Crypto = aesCrypto

	// BUG-3 修复：密钥一致性日志
	// runExecMode 客户端使用 constants.DefaultExecModeAESKey 硬编码密钥，
	// 服务端使用 YAML shell.encrypt.key；如果两者不一致，所有指令模式请求都会解密失败。
	if cfg.Shell.Encrypt.Key == myconstant.DefaultExecModeAESKey {
		logger.Info("AES密钥已配置，与客户端指令模式默认密钥一致")
	} else {
		logger.Warn("AES密钥与客户端指令模式默认密钥不一致，无法支持指令模式调用")
	}

	// ========== 核心服务层初始化 ==========

	// 归一化 Nacos namespace：public 命名空间 ID 是空字符串，不是字面量 "public"
	// （NacosClient 内部也会做同样处理，这里保持 ConfigService 侧一致）
	nacosNamespace := strings.TrimSpace(cfg.Nacos.Namespace)
	if nacosNamespace == "public" || nacosNamespace == "Public" {
		nacosNamespace = ""
	}

	// 配置管理服务
	configSvc := service.NewConfigService(cc, se, service.ConfigServiceParams{
		AgentID:             cfg.Agent.ID,
		AgentGroup:          cfg.Agent.Group,
		Namespace:           nacosNamespace,
		NacosGroup:          cfg.Nacos.Group,
		AgentExecDir:        GetExecDir(), // PRD 3.2.3 reloadScript 工作目录为 Agent 二进制目录
		ReloadScriptTimeout: cfg.Config.ReloadScript.Timeout,
		CleanOrphanFile:     cfg.Config.CleanOrphanFile,
	})

	// 指令执行服务
	cmdSvc := service.NewCommandService(se, cryptoImpl)

	// 进程守护服务（开关关闭则不创建/不启动）
	guardianCfgPath := BootstrapPath("./crontab.yml")
	var guardianSvc *service.GuardianService
	if guardianOn {
		guardianSvc = service.NewGuardianService(se, guardianCfgPath,
			cfg.Crontab.Interval,
			cfg.Crontab.HealthCheck.Timeout,
			cfg.Crontab.StartScript.Timeout,
		)
	} else {
		logger.Info("进程守护服务已禁用，跳过初始化")
	}

	// 定时任务服务（开关关闭则不创建/不启动）
	scheduleCfgPath := BootstrapPath("./scheduledConfig.yml")
	var scheduleSvc *service.ScheduleService
	if scheduleOn {
		// 组装VictoriaMetrics请求头列表：优先使用新headers字段，兼容旧authHeader
		var vmHeaders []model.HeaderKV
		if len(cfg.VictoriaMetrics.Headers) > 0 {
			vmHeaders = cfg.VictoriaMetrics.Headers
		} else if cfg.VictoriaMetrics.AuthHeader != "" {
			// 兼容旧配置：authHeader → Authorization头
			vmHeaders = []model.HeaderKV{
				{Key: "Authorization", Value: cfg.VictoriaMetrics.AuthHeader},
			}
		}
		scheduleSvc = service.NewScheduleService(cfg.VictoriaMetrics.URL, vmHeaders, scheduleCfgPath)
	} else {
		logger.Info("定时任务调度服务已禁用，跳过初始化")
	}

	// 透传服务
	forwardSvc := service.NewForwardService(time.Duration(cfg.Forward.Timeout) * time.Second)
	cleanupFuncs = append(cleanupFuncs, func() {
		forwardSvc.Close()
	})

	// 文件上传服务（PRD 3.8）
	// 传入 GetExecDir() 缓存的可执行文件目录，避免每次请求 os.Executable 系统调用
	uploadSvc := service.NewUploadService(cfg.Upload.MaxFileSize, cfg.Upload.SensitivePaths, GetExecDir())

	// ========== 配置分发（仅Nacos启用时） ==========
	if nacosOn {
		// 启动定时配置拉取循环
		// PullInterval 小于 0 时已在 fillDefaults 钳制为默认 1 分钟
		// 配置清理由 DistributeAllConfigs 内部按 PRD 3.2.4 规则自动触发，不独立起协程
		configSvc.StartPullLoop(cfg.Config.PullInterval)
		cleanupFuncs = append(cleanupFuncs, func() {
			// 停止定时拉取循环即可；Nacos 监听随 cc.Close() 自动失效，无需主动逐个 CancelListener
			configSvc.StopPullLoop()
		})
	}

	// ========== 启动后台协程（仅开关启用时） ==========

	// 进程守护
	if guardianOn && guardianSvc != nil {
		guardianSvc.Start()
		cleanupFuncs = append(cleanupFuncs, func() {
			guardianSvc.Stop()
		})
	}

	// 定时任务
	if scheduleOn && scheduleSvc != nil {
		if err := scheduleSvc.LoadTasks(); err != nil {
			logger.Warn("加载定时任务失败", "error", err)
		} else if err := scheduleSvc.Start(); err != nil {
			logger.Warn("启动定时任务调度器失败", "error", err)
		}
		cleanupFuncs = append(cleanupFuncs, func() {
			scheduleSvc.Stop()
		})
	}

	// ========== HTTP路由注册 ==========
	mux := http.NewServeMux()

	// 健康检查
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(model.HealthResponse{
			Status:     "ok",
			AgentID:    cfg.Agent.ID,
			AgentGroup: cfg.Agent.Group,
			Version:    myconstant.AppVersion,
			Timestamp:  time.Now().Unix(),
		})
	})

	// 远程命令执行
	mux.Handle("/api/v1/exec", http.HandlerFunc(cmdSvc.ExecHandler))

	// 请求透传
	mux.Handle("/forward", http.HandlerFunc(forwardSvc.ForwardHandler))

	// 文件上传（PRD 3.8）
	mux.Handle(myconstant.RouteUpload, http.HandlerFunc(uploadSvc.UploadHandler))

	// 进程守护控制接口（GET ?action=pause/resume/status）
	// 仅支持 GET；guardianSvc 为 nil 时（feature 开关禁用）返回 503
	mux.Handle(myconstant.RouteGuardianControl, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, model.ErrorResponse{
				Code:    http.StatusMethodNotAllowed,
				Message: "仅支持 GET 方法",
			})
			return
		}

		// GuardianService 未启用（feature 开关关闭）
		if guardianSvc == nil {
			writeJSON(w, http.StatusServiceUnavailable, model.ErrorResponse{
				Code:    http.StatusServiceUnavailable,
				Message: "进程守护服务未启用",
			})
			return
		}

		action := r.URL.Query().Get("action")
		if action == "" {
			action = "status" // 默认查询状态
		}

		switch action {
		case "pause":
			if !guardianSvc.IsRunning() {
				writeJSON(w, http.StatusBadRequest, model.ErrorResponse{
					Code:    http.StatusBadRequest,
					Message: "守护服务未启动，无法暂停",
				})
				return
			}
			guardianSvc.Pause()
			writeJSON(w, http.StatusOK, map[string]any{
				"success": true,
				"status":  guardianSvc.Status(),
			})

		case "resume":
			if !guardianSvc.IsRunning() {
				writeJSON(w, http.StatusBadRequest, model.ErrorResponse{
					Code:    http.StatusBadRequest,
					Message: "守护服务未启动，无法恢复",
				})
				return
			}
			guardianSvc.Resume()
			writeJSON(w, http.StatusOK, map[string]any{
				"success": true,
				"status":  guardianSvc.Status(),
			})

		case "status":
			writeJSON(w, http.StatusOK, map[string]any{
				"status": guardianSvc.Status(),
			})

		default:
			writeJSON(w, http.StatusBadRequest, model.ErrorResponse{
				Code:    http.StatusBadRequest,
				Message: fmt.Sprintf("未知 action: %s，可选值: pause, resume, status", action),
			})
		}
	}))

	// 全局鉴权中间件
	handler := service.AuthMiddleware(cfg.Auth.Key, mux)

	// ========== 创建HTTP Server ==========
	// WriteTimeout 需覆盖最长处理器耗时，避免长耗时响应被提前掐断：
	//   远程命令执行最长 MaxExecTimeout(600s)，响应在执行完成后才写入
	//   请求透传最长 MaxForwardTimeout(120s)，响应依赖目标服务返回
	// 取最大值再加余量；此前硬编码 60s 会使超时配置形同虚设（如 forward.timeout=90 时客户端收到连接重置）
	writeTimeout := time.Duration(myconstant.MaxExecTimeout)*time.Second + 10*time.Second
	srv := &http.Server{
		Addr:         bindAddr,
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: writeTimeout,
		IdleTimeout:  120 * time.Second,
	}

	// 汇总已启用的服务（用于日志排查）
	var enabled, disabled []string
	if nacosOn {
		enabled = append(enabled, "nacos")
	} else {
		disabled = append(disabled, "nacos")
	}
	if guardianOn {
		enabled = append(enabled, "guardian")
	} else {
		disabled = append(disabled, "guardian")
	}
	if scheduleOn {
		enabled = append(enabled, "schedule")
	} else {
		disabled = append(disabled, "schedule")
	}
	logger.Info("服务初始化完成",
		"addr", bindAddr,
		"agent_id", cfg.Agent.ID,
		"enabled", enabled,
		"disabled", disabled,
	)

	return srv, cleanup, nil
}

// writeJSON 通用 JSON 响应写入辅助函数
// 设置 Content-Type + status code + 编码写入 body
func writeJSON(w http.ResponseWriter, code int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		logger.Error("写入 JSON 响应失败", "error", err)
	}
}
