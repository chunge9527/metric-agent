package bootstrap

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"metric-agent/internal/common/logger"
)

// WaitForShutdown 等待系统信号并执行优雅关闭
// 监听 SIGINT、SIGTERM，收到信号后：
//
//	第一阶段：关闭 HTTP 服务（10 秒独立超时）
//	第二阶段：执行 cleanup 函数（15 秒独立超时，不被 HTTP Shutdown 消耗）
//
// 任何阶段超时都会触发强制 os.Exit(1)，避免进程僵死
func WaitForShutdown(httpSrv *http.Server, stopFuncs ...func()) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	sig := <-sigCh
	logger.Info("收到退出信号", "signal", sig.String())

	// ============================================================
	// 第一阶段：关闭 HTTP 服务（独占 10 秒）
	// ============================================================
	if httpSrv != nil {
		// 给 HTTP Shutdown 设独立 10 秒超时
		httpTimeout := time.AfterFunc(10*time.Second, func() {
			logger.Warn("HTTP 服务关闭超时（10s），强制禁用 keep-alive 阻止新连接")
			httpSrv.SetKeepAlivesEnabled(false)
		})

		logger.Info("关闭 HTTP 服务...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := httpSrv.Shutdown(shutdownCtx); err != nil {
			logger.Error("HTTP 服务关闭失败", "error", err)
		} else {
			logger.Info("HTTP 服务已关闭")
		}
		cancel()
		httpTimeout.Stop()
	}

	// ============================================================
	// 第二阶段：执行 cleanup（独立 15 秒总超时，不被 HTTP Shutdown 消耗）
	// ============================================================
	cleanupTimeout := time.AfterFunc(15*time.Second, func() {
		logger.Warn("cleanup 超时（15s），强制退出")
		os.Exit(1)
	})
	defer cleanupTimeout.Stop()

	// 每个清理函数独立超时（3s），避免单个函数（如 Nacos CancelListener/CloseClient）
	// 阻塞导致整个 cleanup 卡满 15s 总超时。
	// 注意：超时仅跳过当前函数，goroutine 仍在后台运行，但进程即将退出，泄漏可接受。
	const singleCleanupTimeout = 3 * time.Second
	for i, stopFn := range stopFuncs {
		logger.Info("执行清理函数", "index", i)
		done := make(chan struct{})
		go func(idx int, fn func()) {
			defer close(done)
			defer func() {
				if r := recover(); r != nil {
					logger.Error("清理函数 panic", "index", idx, "error", fmt.Sprint(r))
				}
			}()
			fn()
		}(i, stopFn)

		select {
		case <-done:
			logger.Info("清理函数执行完成", "index", i)
		case <-time.After(singleCleanupTimeout):
			logger.Warn("清理函数执行超时，跳过", "index", i, "timeout", singleCleanupTimeout)
		}
	}

	logger.Info("优雅关闭完成，服务关闭")
	logger.Close() // 所有 shutdown 日志写入完成后再关闭日志文件，确保最后一条日志入文件
	os.Exit(0)
}
