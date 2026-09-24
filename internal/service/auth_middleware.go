// Package service 核心服务层
package service

import (
	"encoding/json"
	"net/http"

	myconstant "metric-agent/internal/common/constant"
	"metric-agent/internal/common/logger"
	"metric-agent/internal/model"
)

// AuthMiddleware HTTP鉴权中间件
// 从请求头读取Authentication和CIB-AUTHORIZATION，与authKey进行大小写敏感精确匹配。
// 路径白名单：
//   - /health    健康检查，探针需免鉴权访问
//   - /metrics   Prometheus metrics 暴露，scrape 端需免鉴权访问
//   - /api/v1/exec 指令执行，ExecHandler 自带 AES 应用层加密保护，
//     知道密钥才能构造有效请求，安全边界在应用层而非 HTTP 头；
//     同时 runExecMode 指令执行模式不加载配置文件，无法获取 auth.key，也应跳过。
//   - /api/v1/guardian 进程守护控制，需免鉴权访问
//     支持 GET ?action=pause / resume / status 三种操作
func AuthMiddleware(authKey string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 路径白名单：健康检查、Prometheus metrics、指令执行、守护控制跳过鉴权
		if r.URL.Path == myconstant.RouteHealth || r.URL.Path == myconstant.RouteMetrics || r.URL.Path == myconstant.RouteExec || r.URL.Path == myconstant.RouteGuardianControl {
			next.ServeHTTP(w, r)
			return
		}

		// 密钥未配置时拒绝所有请求（避免空字符串匹配绕过鉴权）
		if authKey == "" {
			logger.Error("鉴权密钥未配置，拒绝请求",
				"remote_addr", r.RemoteAddr,
				"path", r.URL.Path,
			)
			rejectUnauthorized(w)
			return
		}

		// 从两个请求头读取
		authHeader := r.Header.Get("Authentication")
		cibAuthHeader := r.Header.Get("CIB-AUTHORIZATION")

		// 任一匹配即通过
		if authHeader == authKey || cibAuthHeader == authKey {
			next.ServeHTTP(w, r)
			return
		}

		// 鉴权失败
		logger.Error("鉴权失败",
			"remote_addr", r.RemoteAddr,
			"method", r.Method,
			"path", r.URL.Path,
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
