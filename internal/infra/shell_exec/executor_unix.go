//go:build !windows

package shell_exec

import (
	"os/exec"
	"sync"
	"syscall"
)

// shellPathOnce 缓存 Unix shell 路径（避免每次执行重复 exec.LookPath）
var (
	shellPathOnce sync.Once
	cachedShell   string
)

// getShellPath 获取 Unix 可用的 shell 路径：优先 bash，不存在则使用 sh
func getShellPath() string {
	shellPathOnce.Do(func() {
		cachedShell = "bash"
		if !commandExists(cachedShell) {
			cachedShell = "sh"
		}
	})
	return cachedShell
}

// getShellFlag 返回 Unix shell 的脚本执行 flag（bash/sh 统一用 -c）
func getShellFlag() string {
	return "-c"
}

// buildCommand 构造 Unix 平台的命令参数
// bash -c 模式下第一个非选项参数会被当作 $0，所以需要加 "_" 占位符
// bash -c "echo $1" _ "hello world" → $0="_", $1="hello world" ✅
func buildCommand(script string, args []string) (cmdArgs []string, envVars []string) {
	// 加 "_" 作为 $0 占位符，让用户的 args 从 $1 开始
	cmdArgs = []string{getShellFlag(), script, "_"}
	cmdArgs = append(cmdArgs, args...)
	return cmdArgs, nil
}

// commandExists 检查命令是否存在（仅 Unix 的 getShellPath 使用）
func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// setProcAttr Unix平台设置进程组属性
func setProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}
}

// killProcess Unix平台杀掉整个进程组（仅负责杀进程，不做 Wait 回收）
// Wait 由 executor.go 的后台 goroutine 独占执行，避免并发调用违反 os/exec 契约
func killProcess(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
