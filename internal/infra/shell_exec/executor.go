// Package shell_exec Shell执行器实现
package shell_exec

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	myconstant "metric-agent/internal/common/constant"
	"metric-agent/internal/common/logger"
	"metric-agent/internal/infra/iface"
)

// ShellExecutor Shell执行器实现
type ShellExecutor struct{}

// NewShellExecutor 创建Shell执行器
func NewShellExecutor() *ShellExecutor {
	return &ShellExecutor{}
}

// Exec 同步执行Shell脚本（工作目录继承进程 cwd）
// 委托给 ExecWithDir(ctx, "", script, args...)
func (s *ShellExecutor) Exec(ctx context.Context, script string, args ...string) (stdout string, stderr string, exitCode int, err error) {
	return s.ExecWithDir(ctx, "", script, args...)
}

// ExecWithDir 同步执行Shell脚本并指定工作目录
// workDir: 指定 cmd.Dir；空字符串则不设置（继承进程 cwd）
// 通过context控制超时，超时后自动终止子进程
// shell 路径 + 执行 flag + 参数传递方式由平台相关 buildCommand 函数实现
// Unix: bash -c "script" _ arg1 arg2 ... ($0="_" 占位, $1 起为用户参数)
// Windows: cmd.exe /C + ARG_0/ARG_1 环境变量注入 + 脚本 $1→%ARG_0% 替换
func (s *ShellExecutor) ExecWithDir(ctx context.Context, workDir, script string, args ...string) (stdout string, stderr string, exitCode int, err error) {
	if strings.TrimSpace(script) == "" {
		return "", "", -1, fmt.Errorf("脚本内容为空")
	}

	// 平台相关的命令构造（处理位置参数传递差异）
	cmdArgs, envVars := buildCommand(script, args)
	cmd := exec.CommandContext(ctx, getShellPath(), cmdArgs...)
	if workDir != "" {
		cmd.Dir = workDir
	}
	if len(envVars) > 0 {
		cmd.Env = append(os.Environ(), envVars...)
	}

	// 使用 cappedWriter 捕获输出，单流上限 MaxShellOutputBytes，防止 OOM
	var outBuf, errBuf cappedWriter
	outBuf.limit = myconstant.MaxShellOutputBytes
	errBuf.limit = myconstant.MaxShellOutputBytes
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	// 设置平台相关的进程属性（Linux/Unix设进程组，Windows不支持）
	setProcAttr(cmd)

	if err := cmd.Start(); err != nil {
		logger.Error("启动Shell命令失败", "error", err)
		return "", "", -1, fmt.Errorf("启动Shell命令失败: %w", err)
	}

	// 等待命令完成或上下文取消
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case <-ctx.Done():
		// 超时或取消，强制终止（平台相关实现，仅杀进程，不做 Wait）
		logger.Warn("Shell命令超时，强制终止")
		killProcess(cmd)

		// 等待后台 goroutine 的 cmd.Wait() 完成回收
		// SIGKILL 后内核保证进程快速退出，Wait 会及时返回；裸 <-done 已被外层 ctx 覆盖边界，无需额外保护
		<-done

		return outBuf.String(), errBuf.String(), -1, ctx.Err()

	case err := <-done:
		stdout = outBuf.String()
		stderr = errBuf.String()
		if outBuf.truncated || errBuf.truncated {
			logger.Warn("Shell命令输出已截断（超出上限）",
				"stdout_len", len(stdout), "stdout_truncated", outBuf.truncated,
				"stderr_len", len(stderr), "stderr_truncated", errBuf.truncated)
		}

		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
				logger.Warn("Shell命令执行完成（非零退出码）", "exitCode", exitCode)
				return stdout, stderr, exitCode, nil
			}
			logger.Error("Shell命令执行异常", "error", err)
			return stdout, stderr, -1, fmt.Errorf("Shell命令执行失败: %w", err)
		}

		exitCode = 0
		logger.Debug("Shell命令执行完成", "exitCode", exitCode, "stdout_len", len(stdout), "stderr_len", len(stderr))
		return stdout, stderr, exitCode, nil
	}
}

// cappedWriter 带大小上限的 io.Writer 实现
// 超过 limit 后静默丢弃多余字节，truncated 标记为 true
type cappedWriter struct {
	buf       bytes.Buffer
	limit     int // 最大字节数
	truncated bool
}

func (w *cappedWriter) Write(p []byte) (n int, err error) {
	if w.limit <= 0 {
		// 无限制模式，直接写
		return w.buf.Write(p)
	}
	remaining := w.limit - w.buf.Len()
	if remaining <= 0 {
		w.truncated = true
		return len(p), nil // 全部丢弃，返回 len(p) 模拟全写成功
	}
	if len(p) > remaining {
		w.buf.Write(p[:remaining])
		w.truncated = true
		return len(p), nil
	}
	return w.buf.Write(p)
}

func (w *cappedWriter) String() string {
	return w.buf.String()
}

// 确保实现ShellExecutor接口
var _ iface.ShellExecutor = (*ShellExecutor)(nil)
