//go:build windows
// +build windows

package shell_exec

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

// argIndexRegex 匹配脚本中的位置参数引用: $1 $2 ... $9 $@ $*
// 不匹配 $0 (脚本名占位)、$PATH (环境变量)、$$ (进程ID) 等非位置参数
var argIndexRegex = regexp.MustCompile(`\$([1-9]|@|\*)`)

// buildCommand 构造 Windows 平台的命令参数
// cmd.exe /C 不支持 bash 的 $1/$2/$@ 位置参数语义，改用环境变量 + 脚本文本替换：
//   1. 将 args 注入为 ARG_0, ARG_1, ... 环境变量
//   2. 脚本中 $1→%ARG_0%, $2→%ARG_1%, $@/$*→所有ARG拼接
//   3. cmdArgs 只包含 ["/C", processedScript]，不再向 cmd.exe 追加 args
func buildCommand(script string, args []string) (cmdArgs []string, envVars []string) {
	if len(args) == 0 {
		return []string{getShellFlag(), script}, nil
	}

	// 注入 ARG_0, ARG_1, ... 环境变量
	for i, arg := range args {
		envVars = append(envVars, fmt.Sprintf("ARG_%d=%s", i, arg))
	}

	// bash $@/$* 语义：展开为所有位置参数（空格分隔）
	allArgsExpansion := buildAllArgsExpansion(len(args))

	// 替换脚本中的位置参数引用
	processedScript := argIndexRegex.ReplaceAllStringFunc(script, func(match string) string {
		switch match {
		case "$@", "$*":
			return allArgsExpansion
		default:
			// $1 → 取数字部分, 转为 %ARG_{index-1}%
			n := int(match[1] - '0') // $1 → 1
			if n >= 1 && n <= len(args) {
				return fmt.Sprintf("%%ARG_%d%%", n-1)
			}
			return match // 超出范围的 $N 保持原样
		}
	})

	return []string{getShellFlag(), processedScript}, envVars
}

// buildAllArgsExpansion 生成所有参数的 cmd.exe 环境变量引用串
// len(args)=3 → "%ARG_0% %ARG_1% %ARG_2%"
func buildAllArgsExpansion(n int) string {
	if n == 0 {
		return ""
	}
	parts := make([]string, n)
	for i := 0; i < n; i++ {
		parts[i] = fmt.Sprintf("%%ARG_%d%%", i)
	}
	return strings.Join(parts, " ")
}

// shellPathOnce 缓存 Windows shell 路径
var (
	shellPathOnce sync.Once
	cachedShell   string
)

// getShellPath 获取 Windows 可用的 shell 路径
// 优先使用 COMSPEC 环境变量（Windows 标准，指向 cmd.exe），找不到则退回 "cmd"（exec.Command 会走 PATH 解析）
func getShellPath() string {
	shellPathOnce.Do(func() {
		if comspec := os.Getenv("COMSPEC"); comspec != "" {
			cachedShell = comspec
		} else {
			cachedShell = "cmd"
		}
	})
	return cachedShell
}

// getShellFlag 返回 Windows shell 的脚本执行 flag
// cmd.exe 用 /C；如果用户配了 PowerShell，flag 也会不同，这里按默认 cmd.exe 返回 /C
func getShellFlag() string {
	return "/C"
}

// setProcAttr 设置进程属性（Windows）
func setProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow: true,
	}
}

// killProcess 终止进程（Windows，仅负责杀进程，不做 Wait 回收）
// 使用 taskkill /F /T /PID 终止整个进程树，确保主进程启动的所有子进程（python/java 后台进程等）都被清理
// Wait 由 executor.go 的后台 goroutine 独占执行，避免并发调用违反 os/exec 契约
func killProcess(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}

	// taskkill /F /T /PID <pid>
	// /F = force kill（否则某些进程不响应）
	// /T = kill tree（包括所有子进程）
	// /PID = 指定进程ID
	pidStr := strconv.Itoa(cmd.Process.Pid)
	killCmd := exec.Command("taskkill", "/F", "/T", "/PID", pidStr)
	killCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	killCmd.Stdout = io.Discard
	killCmd.Stderr = io.Discard

	if err := killCmd.Run(); err != nil {
		// taskkill 失败（如进程已退出 0x80070057 "参数错误"），退回到 Process.Kill
		// taskkill 杀进程树失败不代表主进程杀不掉
		_ = cmd.Process.Kill()
		return fmt.Errorf("taskkill /F /T /PID %s 失败: %v (已退回到 Process.Kill)", pidStr, err)
	}
	return nil
}
