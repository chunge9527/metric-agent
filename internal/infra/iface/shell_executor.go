package iface

import "context"

// ShellExecutor Shell执行器接口
// 封装Shell脚本执行能力，支持超时控制与参数数组传递
type ShellExecutor interface {
	// Exec 同步执行Shell脚本（工作目录继承进程 cwd）
	// ctx: 上下文，用于控制执行超时
	// script: 待执行的Shell脚本字符串
	// args: 可选的脚本参数数组，通过 shell -c "script" args... 方式传递，
	//       保留原始 shell 语义（含空格参数不丢失），脚本内可用 $@ 或 $1 $2 引用
	// 返回: 标准输出、标准错误、进程退出码、错误
	Exec(ctx context.Context, script string, args ...string) (stdout string, stderr string, exitCode int, err error)

	// ExecWithDir 同步执行Shell脚本并指定工作目录（PRD 3.2.3 reloadScript 工作目录为 Agent 二进制目录）
	// workDir: 指定脚本执行时的工作目录；空字符串则回退到进程 cwd
	// 其他参数同 Exec
	ExecWithDir(ctx context.Context, workDir, script string, args ...string) (stdout string, stderr string, exitCode int, err error)
}
