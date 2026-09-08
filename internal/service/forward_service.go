package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	myconstant "metric-agent/internal/common/constant"
	"metric-agent/internal/common/logger"
	"metric-agent/internal/model"
)

// hopByHopHeaders Hop-by-hop头列表，转发前需清理
var hopByHopHeaders = []string{
	"Connection",
	"Proxy-Connection",
	"Keep-Alive",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"Te",
	"Trailers",
	"Transfer-Encoding",
	"Upgrade",
}

// ForwardService 请求透传服务
type ForwardService struct {
	defaultTimeout time.Duration
	client         *http.Client
}

// NewForwardService 创建请求透传服务
func NewForwardService(timeout time.Duration) *ForwardService {
	if timeout <= 0 {
		timeout = time.Duration(myconstant.DefaultForwardTimeout) * time.Second
	}
	if timeout > time.Duration(myconstant.MaxForwardTimeout)*time.Second {
		timeout = time.Duration(myconstant.MaxForwardTimeout) * time.Second
	}
	return &ForwardService{
		defaultTimeout: timeout,
		client: &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				MaxIdleConns:          100,
				MaxIdleConnsPerHost:   20,
				IdleConnTimeout:       90 * time.Second,
				ResponseHeaderTimeout: timeout,
			},
		},
	}
}

// Close 关闭透传服务，释放空闲连接
func (s *ForwardService) Close() {
	if s.client != nil && s.client.Transport != nil {
		if t, ok := s.client.Transport.(*http.Transport); ok {
			t.CloseIdleConnections()
		}
	}
}

// ForwardHandler 请求透传处理
// 收到请求后校验 target 参数（URL 格式 + scheme + host），校验通过后直接用 http.Client 转发请求
func (s *ForwardService) ForwardHandler(w http.ResponseWriter, r *http.Request) {
	// 限制转发请求体大小 —— 与 CommandService 保持一致（MaxRequestBodyBytes=10MB）
	// 防止恶意客户端发送超大 body 耗尽内存
	r.Body = http.MaxBytesReader(w, r.Body, myconstant.MaxRequestBodyBytes)

	// 1. 获取并校验 target 参数
	targetStr := r.URL.Query().Get("target")
	if targetStr == "" {
		s.writeErrorJSON(w, http.StatusBadRequest, "缺少target参数")
		return
	}

	targetURL, err := s.validateTarget(targetStr)
	if err != nil {
		logger.Error("target校验失败", "target", targetStr, "error", err)
		s.writeErrorJSON(w, http.StatusForbidden, err.Error())
		return
	}

	// 2. 构造转发请求（复制 method / header / body）
	ctx, cancel := context.WithTimeout(r.Context(), s.defaultTimeout)
	defer cancel()

	fwdReq, err := http.NewRequestWithContext(ctx, r.Method, targetURL.String(), r.Body)
	if err != nil {
		logger.Error("构造转发请求失败", "target", targetStr, "error", err)
		s.writeErrorJSON(w, http.StatusInternalServerError, "构造转发请求失败")
		return
	}

	// 复制请求头（清理 Hop-by-hop + Content-Length，让 Transport 自行处理）
	for k, vs := range r.Header {
		if isHopByHop(k) || strings.EqualFold(k, "Content-Length") {
			continue
		}
		for _, v := range vs {
			fwdReq.Header.Add(k, v)
		}
	}

	// 3. 发送请求
	resp, err := s.client.Do(fwdReq)
	if err != nil {
		// MaxBytesReader 触发：请求体超限
		if isBodyTooLarge(err) {
			s.writeErrorJSON(w, http.StatusRequestEntityTooLarge,
				fmt.Sprintf("请求体超过大小限制 %d 字节", myconstant.MaxRequestBodyBytes))
			return
		}
		logger.Error("请求透传失败", "target", targetStr, "error", err)
		s.writeErrorJSON(w, classifyError(err), err.Error())
		return
	}
	defer resp.Body.Close()

	// 4. 复制响应头
	for k, vs := range resp.Header {
		if isHopByHop(k) {
			continue
		}
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}

	w.WriteHeader(resp.StatusCode)

	// 5. 流式复制响应体
	if _, err := copyBody(w, resp.Body); err != nil {
		logger.Error("写入响应体失败", "target", targetStr, "error", err)
		return
	}

	logger.Info("请求透传完成",
		"method", r.Method,
		"target", targetStr,
		"status", resp.StatusCode,
	)
}

// validateTarget 校验 target URL 的合法性
// 规则：必须是 http/https scheme，host 非空
func (s *ForwardService) validateTarget(targetStr string) (*url.URL, error) {
	targetURL, err := url.Parse(targetStr)
	if err != nil {
		return nil, fmt.Errorf("非法的目标地址")
	}

	if targetURL.Scheme != "http" && targetURL.Scheme != "https" {
		return nil, fmt.Errorf("仅允许http/https协议")
	}

	host := targetURL.Hostname()
	if host == "" {
		return nil, fmt.Errorf("无效的目标主机")
	}

	return targetURL, nil
}

// isHopByHop 判断是否为 Hop-by-hop 头
func isHopByHop(name string) bool {
	for _, h := range hopByHopHeaders {
		if strings.EqualFold(h, name) {
			return true
		}
	}
	return false
}

// writeErrorJSON 写入JSON错误响应
func (s *ForwardService) writeErrorJSON(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(model.ErrorResponse{
		Code:    code,
		Message: message,
	})
}

// classifyError 将请求错误分类为对应 HTTP 状态码
func classifyError(err error) int {
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return http.StatusGatewayTimeout // 504
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return http.StatusBadGateway // 502
	}
	return http.StatusBadGateway
}

// copyBody 流式复制响应体到 ResponseWriter
func copyBody(w http.ResponseWriter, r io.Reader) (int64, error) {
	return io.Copy(w, r)
}
