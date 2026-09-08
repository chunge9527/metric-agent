//go:build linux || darwin || freebsd || openbsd || netbsd || dragonfly

// Package filekit 文件操作工具集
package filekit

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// DiskAvailable 查询指定路径所在磁盘的剩余可用空间（字节）
// path 必须是一个存在的路径（文件或目录均可），否则返回错误
// 跨平台实现：linux 用 unix.Statfs，windows 用 syscall.GetDiskFreeSpaceEx
func DiskAvailable(path string) (int64, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return 0, fmt.Errorf("statfs 失败: %w", err)
	}
	// Bavail 普通用户可用块数 × Bsize 块大小
	// 注意：用 Bavail（可用给普通用户）而非 Bfree（总空闲），避免 root 预留块被误计
	return int64(stat.Bavail) * int64(stat.Bsize), nil
}


