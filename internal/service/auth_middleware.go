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

// authRealm HTTP Basic 认证 realm 值，PRD 3.8 规定固定文案
const authRealm = `Basic realm="Please input username and password"`

// AuthMiddleware HTTP Basic 认证中间件（PRD 3.8）
//
// 认证流程：
//  1. 路径白名单直接放行（/health /api/v1/exec /api/v1/guardian）。
//  2. username/password 配置 trim 后为空 → 401（未配置鉴权，兜底拒绝所有请求）。
//  3. 使用 r.BasicAuth() 解析 Authorization 头（Go 标准库自动处理 Basic 前缀、base64 解码、user:pass 拆分）。
//     - ok=false 表示请求未携带合法的 Basic 头（无 Authorization、无 Basic 前缀、base64 非法、无冒号分隔）→ 401。
//     - ok=true 但 user 或 pass trim 后为空（如 base64(":password") 或 base64("user:")）→ 401。
//  4. user == username && pass == password（精确、大小写敏感匹配）→ 放行；否则 401。
//
// 所有 401 响应均携带 WWW-Authenticate: Basic realm="Please input username and password"（PRD 3.8 强制要求）。
//
// 作者：王春
// 日期：2026-09-29
func AuthMiddleware(username, password string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 路径白名单：健康检查、指令执行、守护控制跳过鉴权；/metrics 需鉴权
		if r.URL.Path == myconstant.RouteHealth || r.URL.Path == myconstant.RouteExec || r.URL.Path == myconstant.RouteGuardianControl {
			next.ServeHTTP(w, r)
			return
		}

		// 配置为空时拒绝所有请求（PRD 3.8：未配置 auth.username 或 auth.password → 直接 401）
		if strings.TrimSpace(username) == "" || strings.TrimSpace(password) == "" {
			logger.Error("鉴权配置未完成（auth.username 或 auth.password 为空），拒绝请求",
				"remote_addr", r.RemoteAddr,
				"path", r.URL.Path,
			)
			rejectUnauthorized(w)
			return
		}

		// 解析 HTTP Basic 认证（Go 标准库自动处理 Authorization: Basic <base64>）
		actualUser, actualPass, ok := r.BasicAuth()
		if !ok {
			logger.Error("未携带合法的 HTTP Basic 认证头",
				"remote_addr", r.RemoteAddr,
				"method", r.Method,
				"path", r.URL.Path,
			)
			rejectUnauthorized(w)
			return
		}

		// BasicAuth 返回 ok=true 但 user 或 pass 为空的边界（如 base64(":pass") 或 base64("user:")）
		// PRD 3.8：值为空字符串也表示未携带认证头 → 401
		if strings.TrimSpace(actualUser) == "" || strings.TrimSpace(actualPass) == "" {
			logger.Error("HTTP Basic 认证头中的用户名或密码为空",
				"remote_addr", r.RemoteAddr,
				"method", r.Method,
				"path", r.URL.Path,
			)
			rejectUnauthorized(w)
			return
		}

		// 精确、大小写敏感匹配（PRD 3.8 业务规则：账号和密码进行精确、大小写敏感匹配）
		if actualUser == username && actualPass == password {
			next.ServeHTTP(w, r)
			return
		}

		// 鉴权失败
		logger.Error("HTTP Basic 鉴权失败",
			"remote_addr", r.RemoteAddr,
			"method", r.Method,
			"path", r.URL.Path,
		)
		rejectUnauthorized(w)
	})
}

// rejectUnauthorized 返回 401 Unauthorized 响应
// 必须在 WriteHeader 之前设置 WWW-Authenticate 响应头（PRD 3.8 强制要求）
func rejectUnauthorized(w http.ResponseWriter) {
	// WWW-Authenticate 必须在 WriteHeader 之前写入，否则不会被发送
	w.Header().Set("WWW-Authenticate", authRealm)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	json.NewEncoder(w).Encode(model.ErrorResponse{
		Code:    http.StatusUnauthorized,
		Message: "未授权访问",
	})
}
