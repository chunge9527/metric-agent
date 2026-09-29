// Package service 鉴权中间件单元测试
// 作者：王春
// 日期：2026-09-29
package service

import (
	"net/http"
	"net/http/httptest"
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
	testHeaderName  = "Authorization"
	testAuthValue   = "metric-agent-auth-key-2024"
	customHeader    = "X-Auth-Token"
	customAuthValue = "my-custom-secret"
)

// ============ 正常鉴权通过 ============

// TestAuthMiddleware_Success 使用默认请求头名，值匹配 → 200
func TestAuthMiddleware_Success(t *testing.T) {
	mw := AuthMiddleware(testHeaderName, testAuthValue, dummyHandler(http.StatusOK))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/guardian", nil)
	req.Header.Set(testHeaderName, testAuthValue)
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("期望 200 OK，实际 %d", rr.Code)
	}
}

// TestAuthMiddleware_Success_CustomHeader 自定义请求头名 → 200
func TestAuthMiddleware_Success_CustomHeader(t *testing.T) {
	mw := AuthMiddleware(customHeader, customAuthValue, dummyHandler(http.StatusOK))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/config/listeners", nil)
	req.Header.Set(customHeader, customAuthValue)
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("自定义请求头名期望 200 OK，实际 %d", rr.Code)
	}
}

// ============ 鉴权失败场景 ============

// TestAuthMiddleware_MissingHeader 未携带鉴权请求头 → 401
func TestAuthMiddleware_MissingHeader(t *testing.T) {
	mw := AuthMiddleware(testHeaderName, testAuthValue, dummyHandler(http.StatusOK))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/config/listeners", nil)
	// 不设置任何鉴权 header
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("缺少 header 期望 401，实际 %d", rr.Code)
	}
}

// TestAuthMiddleware_WrongValue 值不匹配 → 401
func TestAuthMiddleware_WrongValue(t *testing.T) {
	mw := AuthMiddleware(testHeaderName, testAuthValue, dummyHandler(http.StatusOK))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/config/listeners", nil)
	req.Header.Set(testHeaderName, "wrong-value")
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("值不匹配期望 401，实际 %d", rr.Code)
	}
}

// TestAuthMiddleware_CaseSensitive 大小写敏感：值相同但大小写不同 → 401
func TestAuthMiddleware_CaseSensitive(t *testing.T) {
	mw := AuthMiddleware(testHeaderName, testAuthValue, dummyHandler(http.StatusOK))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/config/listeners", nil)
	// 将 auth value 首字母大写，验证大小写敏感
	req.Header.Set(testHeaderName, "Metric-Agent-Auth-Key-2024")
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("大小写敏感期望 401，实际 %d", rr.Code)
	}
}

// TestAuthMiddleware_WrongHeaderName 请求头名不对（配置的是 Authorization，请求里放 Authentication）→ 401
func TestAuthMiddleware_WrongHeaderName(t *testing.T) {
	mw := AuthMiddleware(testHeaderName, testAuthValue, dummyHandler(http.StatusOK))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/config/listeners", nil)
	req.Header.Set("Authentication", testAuthValue) // 不是配置的 header 名
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("header 名不对期望 401，实际 %d", rr.Code)
	}
}

// ============ 配置为空场景（运行时兜底） ============

// TestAuthMiddleware_EmptyHeaderName headerName 为空 → 401
func TestAuthMiddleware_EmptyHeaderName(t *testing.T) {
	mw := AuthMiddleware("", testAuthValue, dummyHandler(http.StatusOK))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/config/listeners", nil)
	req.Header.Set("Authorization", testAuthValue)
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("headerName 为空期望 401，实际 %d", rr.Code)
	}
}

// TestAuthMiddleware_EmptyExpectedValue expectedValue 为空 → 401
func TestAuthMiddleware_EmptyExpectedValue(t *testing.T) {
	mw := AuthMiddleware(testHeaderName, "", dummyHandler(http.StatusOK))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/config/listeners", nil)
	req.Header.Set(testHeaderName, "anything")
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expectedValue 为空期望 401，实际 %d", rr.Code)
	}
}

// ============ 请求头值为空场景（不进入 == 匹配） ============

// TestAuthMiddleware_ActualValueEmpty 请求头存在但值为空字符串 → 401
func TestAuthMiddleware_ActualValueEmpty(t *testing.T) {
	mw := AuthMiddleware(testHeaderName, testAuthValue, dummyHandler(http.StatusOK))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/config/listeners", nil)
	req.Header.Set(testHeaderName, "") // header 存在但值为空串
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("请求头值为空串期望 401，实际 %d", rr.Code)
	}
}

// TestAuthMiddleware_ActualValueWhitespace 请求头存在但值为纯空格 → 401
func TestAuthMiddleware_ActualValueWhitespace(t *testing.T) {
	mw := AuthMiddleware(testHeaderName, testAuthValue, dummyHandler(http.StatusOK))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/config/listeners", nil)
	req.Header.Set(testHeaderName, "   \t  ") // 纯空白
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("请求头值为纯空格期望 401，实际 %d", rr.Code)
	}
}

// ============ 路径白名单 ============

// TestAuthMiddleware_Whitelist_Health /health 跳过鉴权
func TestAuthMiddleware_Whitelist_Health(t *testing.T) {
	mw := AuthMiddleware(testHeaderName, testAuthValue, dummyHandler(http.StatusOK))
	req := httptest.NewRequest(http.MethodGet, myconstant.RouteHealth, nil)
	// 不携带任何鉴权 header
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("/health 白名单期望 200，实际 %d", rr.Code)
	}
}

// TestAuthMiddleware_Whitelist_Exec /api/v1/exec 跳过鉴权
func TestAuthMiddleware_Whitelist_Exec(t *testing.T) {
	mw := AuthMiddleware(testHeaderName, testAuthValue, dummyHandler(http.StatusOK))
	req := httptest.NewRequest(http.MethodPost, myconstant.RouteExec, nil)
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("/api/v1/exec 白名单期望 200，实际 %d", rr.Code)
	}
}

// TestAuthMiddleware_Whitelist_Guardian /api/v1/guardian 跳过鉴权
func TestAuthMiddleware_Whitelist_Guardian(t *testing.T) {
	mw := AuthMiddleware(testHeaderName, testAuthValue, dummyHandler(http.StatusOK))
	req := httptest.NewRequest(http.MethodGet, myconstant.RouteGuardianControl, nil)
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("/api/v1/guardian 白名单期望 200，实际 %d", rr.Code)
	}
}

// TestAuthMiddleware_Metrics_NeedAuth /metrics 不在白名单，需鉴权
func TestAuthMiddleware_Metrics_NeedAuth(t *testing.T) {
	mw := AuthMiddleware(testHeaderName, testAuthValue, dummyHandler(http.StatusOK))
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	// 不携带鉴权 header
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("/metrics 需鉴权但未携带，期望 401，实际 %d", rr.Code)
	}

	// 带上正确 header 后应通过
	req2 := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req2.Header.Set(testHeaderName, testAuthValue)
	rr2 := httptest.NewRecorder()
	mw.ServeHTTP(rr2, req2)

	if rr2.Code != http.StatusOK {
		t.Errorf("/metrics 携带正确凭证期望 200，实际 %d", rr2.Code)
	}
}
