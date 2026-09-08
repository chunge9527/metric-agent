package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	myconstant "metric-agent/internal/common/constant"
	"metric-agent/internal/common/logger"
	"metric-agent/internal/infra/iface"
	"metric-agent/internal/model"
)

// CommandService 指令执行服务
type CommandService struct {
	se     iface.ShellExecutor
	crypto iface.Crypto
}

// NewCommandService 创建指令执行服务
func NewCommandService(se iface.ShellExecutor, crypto iface.Crypto) *CommandService {
	return &CommandService{
		se:     se,
		crypto: crypto,
	}
}

// ExecHandler HTTP命令执行处理器
// 统一使用 AES 加解密：请求体 AES.Decrypt → 解析 JSON → 执行脚本 → 响应 AES.Encrypt
// PRD 2.2 变更：删除 executeType URL 参数，local 模式与 remote 模式统一 AES 加密传输
func (s *CommandService) ExecHandler(w http.ResponseWriter, r *http.Request) {
	// 仅接受 POST 方法
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "仅支持 POST 方法")
		return
	}
	// 读取请求体（LimitReader 防 OOM）
	body, err := readAll(r)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "读取请求体失败")
		return
	}
	defer r.Body.Close()

	// 请求体 AES 解密
	plaintext, err := s.crypto.Decrypt(body)
	if err != nil {
		logger.Error("请求体解密失败", "error", err)
		s.writeError(w, http.StatusBadRequest, "请求体解密失败")
		return
	}

	// 解析 ExecRequest
	var req model.ExecRequest
	if err := json.Unmarshal(plaintext, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, "请求体JSON解析失败")
		return
	}

	// 参数校验
	if req.Script == "" || isBlank(req.Script) {
		s.writeError(w, http.StatusBadRequest, "脚本内容不能为空")
		return
	}

	// 超时参数校验（PRD 3.3.1：传入≤0或>600秒时拒绝执行）
	timeout := myconstant.DefaultExecTimeout
	if req.Timeout != nil {
		if *req.Timeout <= 0 || *req.Timeout > myconstant.MaxExecTimeout {
			s.writeError(w, http.StatusBadRequest,
				fmt.Sprintf("超时时间非法：必须大于0且不超过%d秒", myconstant.MaxExecTimeout))
			return
		}
		timeout = *req.Timeout
	}

	// 执行脚本
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(timeout)*time.Second)
	defer cancel()

	startTime := time.Now()
	stdout, stderr, exitCode, err := s.se.Exec(ctx, req.Script, req.Args...)
	duration := time.Since(startTime).Milliseconds()

	// 封装执行结果
	resp := model.ExecResponse{
		Stdout:   stdout,
		Stderr:   stderr,
		ExitCode: exitCode,
		Duration: duration,
	}

	// 超时处理
	if err == context.DeadlineExceeded {
		logger.Error("命令执行超时", "timeout", timeout)
		s.writeEncryptedResponse(w, http.StatusRequestTimeout, resp)
		return
	}

	if err != nil {
		logger.Error("命令执行失败", "error", err)
		s.writeError(w, http.StatusInternalServerError, "命令执行失败")
		return
	}

	// 返回执行结果（AES 加密）
	s.writeEncryptedResponse(w, http.StatusOK, resp)

	logger.Info("命令执行完成",
		"duration_ms", duration,
		"exit_code", exitCode,
	)
}

// writeEncryptedResponse 将 ExecResponse AES 加密后写入响应体
func (s *CommandService) writeEncryptedResponse(w http.ResponseWriter, code int, resp model.ExecResponse) {
	respJSON, err := json.Marshal(resp)
	if err != nil {
		logger.Error("响应序列化失败", "error", err)
		s.writeError(w, http.StatusInternalServerError, "响应序列化失败")
		return
	}

	encrypted, err := s.crypto.Encrypt(respJSON)
	if err != nil {
		logger.Error("响应加密失败", "error", err)
		s.writeError(w, http.StatusInternalServerError, "响应加密失败")
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.WriteHeader(code)
	w.Write(encrypted)
}

// writeError 写入 AES 加密的错误响应
func (s *CommandService) writeError(w http.ResponseWriter, code int, message string) {
	errResp := model.ErrorResponse{
		Code:    code,
		Message: message,
	}

	respJSON, err := json.Marshal(errResp)
	if err != nil {
		logger.Error("错误响应序列化失败", "error", err)
		// 降级明文返回
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		json.NewEncoder(w).Encode(errResp)
		return
	}

	encrypted, err := s.crypto.Encrypt(respJSON)
	if err != nil {
		logger.Error("错误响应加密失败", "error", err)
		// 降级明文返回
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		json.NewEncoder(w).Encode(errResp)
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.WriteHeader(code)
	w.Write(encrypted)
}

// readAll 读取 HTTP 请求体，LimitReader 防 OOM
func readAll(r *http.Request) ([]byte, error) {
	limited := io.LimitReader(r.Body, myconstant.MaxRequestBodyBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if len(data) > myconstant.MaxRequestBodyBytes {
		return nil, fmt.Errorf("请求体超过大小限制 %d 字节", myconstant.MaxRequestBodyBytes)
	}
	return data, nil
}

// isBlank 检查字符串是否全部为空白
func isBlank(s string) bool {
	for _, c := range s {
		if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			return false
		}
	}
	return true
}
