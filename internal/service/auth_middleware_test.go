// Package service 鉴权中间件单元测试 —— HTTP Basic 认证（PRD 3.8）
// 作者：王春
// 日期：2026-09-29
package service

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	myconstant "metric-agent/internal/common/constant"
)

// dummyHandler 用于验证中间件是否放行请求
func dummyHandler(statusCode int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(statusCode)
	})
}

const (
	testUsername = "metric-agent-user"
	testPassword = "metric-agent-pass-2024"
)

// assertWWWAuthenticateOK 校验 401 响应携带正确的 WWW-Authenticate 头
func assertWWWAuthenticateOK(t *testing.T, rr *httptest.ResponseRecorder) {
	t.Helper()
	got := rr.Header().Get("WWW-Authenticate")
	if got != authRealm {
		t.Errorf("WWW-Authenticate 响应头期望 %q，实际 %q", authRealm, got)
	}
}

// ============ 正常鉴权通过 ============

// TestAuthMiddleware_BasicAuth_Success HTTP Basic 认证正确账号密码 → 200
func TestAuthMiddleware_BasicAuth_Success(t *testing.T) {
	mw := AuthMiddleware(testUsername, testPassword, dummyHandler(http.StatusOK))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/config/listeners", nil)
	req.SetBasicAuth(testUsername, testPassword)
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("期望 200 OK，实际 %d", rr.Code)
	}
}

// ============ 鉴权失败场景（PRD 3.8） ============

// TestAuthMiddleware_BasicAuth_NoHeader 未携带 Authorization 头 → 401 + WWW-Authenticate
func TestAuthMiddleware_BasicAuth_NoHeader(t *testing.T) {
	mw := AuthMiddleware(testUsername, testPassword, dummyHandler(http.StatusOK))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/config/listeners", nil)
	// 不设置任何 Authorization 头
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("无 Authorization 头期望 401，实际 %d", rr.Code)
	}
	assertWWWAuthenticateOK(t, rr)
}

// TestAuthMiddleware_BasicAuth_WrongScheme Authorization 头不带 "Basic " 前缀（如 Bearer）→ 401
func TestAuthMiddleware_BasicAuth_WrongScheme(t *testing.T) {
	mw := AuthMiddleware(testUsername, testPassword, dummyHandler(http.StatusOK))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/config/listeners", nil)
	req.Header.Set("Authorization", "Bearer some-token") // 不是 Basic
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("Bearer scheme 期望 401，实际 %d", rr.Code)
	}
	assertWWWAuthenticateOK(t, rr)
}

// TestAuthMiddleware_BasicAuth_InvalidBase64 Basic 后面的 base64 非法 → 401
func TestAuthMiddleware_BasicAuth_InvalidBase64(t *testing.T) {
	mw := AuthMiddleware(testUsername, testPassword, dummyHandler(http.StatusOK))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/config/listeners", nil)
	req.Header.Set("Authorization", "Basic !!!not-valid-base64!!!")
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("非法 base64 期望 401，实际 %d", rr.Code)
	}
	assertWWWAuthenticateOK(t, rr)
}

// TestAuthMiddleware_BasicAuth_NoColon base64 解码后无冒号分隔（无 user:pass 格式）→ 401
func TestAuthMiddleware_BasicAuth_NoColon(t *testing.T) {
	mw := AuthMiddleware(testUsername, testPassword, dummyHandler(http.StatusOK))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/config/listeners", nil)
	// base64("no-colon-string") → 解码后没有冒号
	b64 := base64.StdEncoding.EncodeToString([]byte("no-colon-string"))
	req.Header.Set("Authorization", "Basic "+b64)
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("无冒号分隔期望 401，实际 %d", rr.Code)
	}
	assertWWWAuthenticateOK(t, rr)
}

// TestAuthMiddleware_BasicAuth_EmptyUser base64(":password") → 用户名空 → 401
func TestAuthMiddleware_BasicAuth_EmptyUser(t *testing.T) {
	mw := AuthMiddleware(testUsername, testPassword, dummyHandler(http.StatusOK))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/config/listeners", nil)
	b64 := base64.StdEncoding.EncodeToString([]byte(":" + testPassword))
	req.Header.Set("Authorization", "Basic "+b64)
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("用户名空期望 401，实际 %d", rr.Code)
	}
	assertWWWAuthenticateOK(t, rr)
}

// TestAuthMiddleware_BasicAuth_EmptyPassword base64("user:") → 密码空 → 401
func TestAuthMiddleware_BasicAuth_EmptyPassword(t *testing.T) {
	mw := AuthMiddleware(testUsername, testPassword, dummyHandler(http.StatusOK))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/config/listeners", nil)
	b64 := base64.StdEncoding.EncodeToString([]byte(testUsername + ":"))
	req.Header.Set("Authorization", "Basic "+b64)
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("密码空期望 401，实际 %d", rr.Code)
	}
	assertWWWAuthenticateOK(t, rr)
}

// TestAuthMiddleware_BasicAuth_AllEmpty base64(":") → 用户名密码都空 → 401
func TestAuthMiddleware_BasicAuth_AllEmpty(t *testing.T) {
	mw := AuthMiddleware(testUsername, testPassword, dummyHandler(http.StatusOK))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/config/listeners", nil)
	b64 := base64.StdEncoding.EncodeToString([]byte(":"))
	req.Header.Set("Authorization", "Basic "+b64)
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("用户名密码都空期望 401，实际 %d", rr.Code)
	}
	assertWWWAuthenticateOK(t, rr)
}

// TestAuthMiddleware_BasicAuth_WrongUsername 账号错误 → 401
func TestAuthMiddleware_BasicAuth_WrongUsername(t *testing.T) {
	mw := AuthMiddleware(testUsername, testPassword, dummyHandler(http.StatusOK))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/config/listeners", nil)
	req.SetBasicAuth("wrong-user", testPassword)
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("账号错误期望 401，实际 %d", rr.Code)
	}
	assertWWWAuthenticateOK(t, rr)
}

// TestAuthMiddleware_BasicAuth_WrongPassword 密码错误 → 401
func TestAuthMiddleware_BasicAuth_WrongPassword(t *testing.T) {
	mw := AuthMiddleware(testUsername, testPassword, dummyHandler(http.StatusOK))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/config/listeners", nil)
	req.SetBasicAuth(testUsername, "wrong-pass")
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("密码错误期望 401，实际 %d", rr.Code)
	}
	assertWWWAuthenticateOK(t, rr)
}

// TestAuthMiddleware_BasicAuth_CaseSensitive 大小写敏感：密码大小写不同 → 401
func TestAuthMiddleware_BasicAuth_CaseSensitive(t *testing.T) {
	mw := AuthMiddleware(testUsername, testPassword, dummyHandler(http.StatusOK))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/config/listeners", nil)
	// 密码首字母大写，验证大小写敏感
	req.SetBasicAuth(testUsername, "Metric-Agent-Pass-2024")
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("大小写敏感期望 401，实际 %d", rr.Code)
	}
	assertWWWAuthenticateOK(t, rr)
}

// TestAuthMiddleware_BasicAuth_BasicPrefixCase Basic 前缀大小写不敏感（Go r.BasicAuth() 内部 strings.EqualFold）
func TestAuthMiddleware_BasicAuth_BasicPrefixCase(t *testing.T) {
	mw := AuthMiddleware(testUsername, testPassword, dummyHandler(http.StatusOK))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/config/listeners", nil)
	// 手动设置 "BASIC " 前缀（大写），Go 标准库内部 EqualFold 处理
	b64 := base64.StdEncoding.EncodeToString([]byte(testUsername + ":" + testPassword))
	req.Header.Set("Authorization", "BASIC "+b64)
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Basic 前缀大写期望 200，实际 %d", rr.Code)
	}
}

// ============ 配置为空场景（运行时兜底） ============

// TestAuthMiddleware_EmptyUsername 配置 Username 为空 → 401
func TestAuthMiddleware_EmptyUsername(t *testing.T) {
	mw := AuthMiddleware("", testPassword, dummyHandler(http.StatusOK))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/config/listeners", nil)
	req.SetBasicAuth("any-user", "any-pass")
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("Username 空期望 401，实际 %d", rr.Code)
	}
	assertWWWAuthenticateOK(t, rr)
}

// TestAuthMiddleware_EmptyPassword 配置 Password 为空 → 401
func TestAuthMiddleware_EmptyPassword(t *testing.T) {
	mw := AuthMiddleware(testUsername, "", dummyHandler(http.StatusOK))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/config/listeners", nil)
	req.SetBasicAuth("any-user", "any-pass")
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("Password 空期望 401，实际 %d", rr.Code)
	}
	assertWWWAuthenticateOK(t, rr)
}

// TestAuthMiddleware_WhitespaceUsername 配置 Username 为纯空白 → 401
func TestAuthMiddleware_WhitespaceUsername(t *testing.T) {
	mw := AuthMiddleware("   \t  ", testPassword, dummyHandler(http.StatusOK))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/config/listeners", nil)
	req.SetBasicAuth(testUsername, testPassword)
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("Username 为纯空白期望 401，实际 %d", rr.Code)
	}
	assertWWWAuthenticateOK(t, rr)
}

// ============ 路径白名单 ============

// TestAuthMiddleware_Whitelist_Health /health 跳过鉴权
func TestAuthMiddleware_Whitelist_Health(t *testing.T) {
	mw := AuthMiddleware(testUsername, testPassword, dummyHandler(http.StatusOK))
	req := httptest.NewRequest(http.MethodGet, myconstant.RouteHealth, nil)
	// 不携带任何鉴权头
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("/health 白名单期望 200，实际 %d", rr.Code)
	}
}

// TestAuthMiddleware_Whitelist_Exec /api/v1/exec 跳过鉴权
func TestAuthMiddleware_Whitelist_Exec(t *testing.T) {
	mw := AuthMiddleware(testUsername, testPassword, dummyHandler(http.StatusOK))
	req := httptest.NewRequest(http.MethodPost, myconstant.RouteExec, nil)
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("/api/v1/exec 白名单期望 200，实际 %d", rr.Code)
	}
}

// TestAuthMiddleware_Whitelist_Guardian /api/v1/guardian 跳过鉴权
func TestAuthMiddleware_Whitelist_Guardian(t *testing.T) {
	mw := AuthMiddleware(testUsername, testPassword, dummyHandler(http.StatusOK))
	req := httptest.NewRequest(http.MethodGet, myconstant.RouteGuardianControl, nil)
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("/api/v1/guardian 白名单期望 200，实际 %d", rr.Code)
	}
}

// TestAuthMiddleware_Metrics_NeedAuth /metrics 不在白名单，需 Basic 认证
func TestAuthMiddleware_Metrics_NeedAuth(t *testing.T) {
	mw := AuthMiddleware(testUsername, testPassword, dummyHandler(http.StatusOK))

	// 不带认证 → 401
	req1 := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rr1 := httptest.NewRecorder()
	mw.ServeHTTP(rr1, req1)
	if rr1.Code != http.StatusUnauthorized {
		t.Errorf("/metrics 无认证期望 401，实际 %d", rr1.Code)
	}
	assertWWWAuthenticateOK(t, rr1)

	// 带正确 Basic 认证 → 200
	req2 := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req2.SetBasicAuth(testUsername, testPassword)
	rr2 := httptest.NewRecorder()
	mw.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Errorf("/metrics 携带正确凭证期望 200，实际 %d", rr2.Code)
	}
}

// ============ Authorization 头存在但值为空字符串 ============

// TestAuthMiddleware_AuthorizationEmpty Authorization 头存在但值为空 → 401（PRD 3.8：值为空字符串也表示未携带）
func TestAuthMiddleware_AuthorizationEmpty(t *testing.T) {
	mw := AuthMiddleware(testUsername, testPassword, dummyHandler(http.StatusOK))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/config/listeners", nil)
	req.Header.Set("Authorization", "") // 头存在但值为空
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("Authorization 头值为空期望 401，实际 %d", rr.Code)
	}
	assertWWWAuthenticateOK(t, rr)
}

// TestAuthMiddleware_AuthorizationWhitespace Authorization 头存在但值为纯空格 → 401
func TestAuthMiddleware_AuthorizationWhitespace(t *testing.T) {
	mw := AuthMiddleware(testUsername, testPassword, dummyHandler(http.StatusOK))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/config/listeners", nil)
	req.Header.Set("Authorization", "   \t  ")
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("Authorization 头值为纯空格期望 401，实际 %d", rr.Code)
	}
	assertWWWAuthenticateOK(t, rr)
}

// TestAuthMiddleware_RealmExact 校验 realm 精确值（无多余空格、引号）
func TestAuthMiddleware_RealmExact(t *testing.T) {
	mw := AuthMiddleware(testUsername, testPassword, dummyHandler(http.StatusOK))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/config/listeners", nil)
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	got := rr.Header().Get("WWW-Authenticate")
	// 必须完全匹配，不能有多余字符
	if strings.TrimSpace(got) != authRealm {
		t.Errorf("realm 值精确校验失败\n期望: %q\n实际: %q", authRealm, got)
	}
}
