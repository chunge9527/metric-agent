// Package service 核心服务层
package service

import (
	"encoding/json"
	"net/http"
	"strings"

	myconstant "metric-agent/internal/common/constant"
	"metric-agent/internal/common/logger"
	"metric-agent/internal/model"
)

// AuthMiddleware HTTP鉴权中间件（PRD 3.8）
// 从请求头 headerName 读取值，与 expectedValue 进行大小写敏感精确匹配。
// headerName 和 expectedValue 均由 metricAgent.yml 中 auth.key / auth.value 配置。
//
// 启动时不校验 auth 配置完整性，运行时中间件自行兜底：
//   - 配置为空（auth.key 或 auth.value TrimSpace 后为空）→ 401
//   - 请求头值为空（header 不存在或 TrimSpace 后为空）→ 401，不做 == 匹配
//   - 仅 actualValue == expectedValue 且两者均非空时才放行
//
// 路径白名单：
//   - /health    健康检查，探针需免鉴权访问
//   - /metrics   Prometheus metrics 暴露，需鉴权（scrape 端携带 auth.key 请求头）
//   - /api/v1/exec 指令执行，ExecHandler 自带 AES 应用层加密保护，
//     知道密钥才能构造有效请求，安全边界在应用层而非 HTTP 头；
//     同时 runExecMode 指令执行模式不加载配置文件，无法获取 auth.key，也应跳过。
//   - /api/v1/guardian 进程守护控制，需免鉴权访问
//     支持 GET ?action=pause / resume / status 三种操作
func AuthMiddleware(headerName, expectedValue string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 路径白名单：健康检查、指令执行、守护控制跳过鉴权；/metrics 需鉴权
		if r.URL.Path == myconstant.RouteHealth || r.URL.Path == myconstant.RouteExec || r.URL.Path == myconstant.RouteGuardianControl {
			next.ServeHTTP(w, r)
			return
		}

		// 配置为空时拒绝所有请求（启动时不校验，运行时兜底；防止 "" == "" 绕过）
		if strings.TrimSpace(headerName) == "" || strings.TrimSpace(expectedValue) == "" {
			logger.Error("鉴权配置未完成（auth.key 或 auth.value 为空），拒绝请求",
				"remote_addr", r.RemoteAddr,
				"path", r.URL.Path,
			)
			rejectUnauthorized(w)
			return
		}

		// 从配置的请求头读取凭证
		actualValue := r.Header.Get(headerName)

		// 请求头值为空（header 不存在或值为空白）直接拒绝，不进入 == 匹配逻辑
		if strings.TrimSpace(actualValue) == "" {
			logger.Error("鉴权请求头缺失或为空",
				"remote_addr", r.RemoteAddr,
				"method", r.Method,
				"path", r.URL.Path,
				"header", headerName,
			)
			rejectUnauthorized(w)
			return
		}

		// 精确、大小写敏感匹配（actualValue 和 expectedValue 均非空）
		if actualValue == expectedValue {
			next.ServeHTTP(w, r)
			return
		}

		// 鉴权失败
		logger.Error("鉴权失败",
			"remote_addr", r.RemoteAddr,
			"method", r.Method,
			"path", r.URL.Path,
			"header", headerName,
		)
		rejectUnauthorized(w)
	})
}

// rejectUnauthorized 返回401未授权响应
func rejectUnauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	json.NewEncoder(w).Encode(model.ErrorResponse{
		Code:    http.StatusUnauthorized,
		Message: "未授权访问",
	})
}
