//go:build windows

// Package filekit 文件操作工具集
package filekit

import (
	"fmt"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

// DiskAvailable 查询指定路径所在磁盘的剩余可用空间（字节）
// path 必须是一个存在的路径（文件或目录均可），否则返回错误
// Windows 实现：用 GetDiskFreeSpaceExW
func DiskAvailable(path string) (int64, error) {
	root, err := diskRoot(path)
	if err != nil {
		return 0, err
	}

	var freeBytes uint64
	var totalBytes uint64
	var totalFree uint64

	rootPtr, err := syscall.UTF16PtrFromString(root)
	if err != nil {
		return 0, fmt.Errorf("路径编码失败: %w", err)
	}

	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	getDiskFreeSpaceEx := kernel32.NewProc("GetDiskFreeSpaceExW")

	_, _, callErr := getDiskFreeSpaceEx.Call(
		uintptr(unsafe.Pointer(rootPtr)),
		uintptr(unsafe.Pointer(&freeBytes)),
		uintptr(unsafe.Pointer(&totalBytes)),
		uintptr(unsafe.Pointer(&totalFree)),
	)
	if callErr != nil {
		return 0, fmt.Errorf("GetDiskFreeSpaceEx 失败: %w", callErr)
	}

	return int64(freeBytes), nil
}



// diskRoot 从路径中提取磁盘根（如 "C:\foo\bar" → "C:\"）
func diskRoot(path string) (string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("路径无法解析: %w", err)
	}

	// Windows 盘符如 "C:\" 或 UNC 路径 "\\server\share\"
	volume := filepath.VolumeName(absPath)
	if volume == "" {
		return "", fmt.Errorf("无法从路径解析磁盘卷: %s", absPath)
	}

	// 确保以 "\" 结尾（GetDiskFreeSpaceEx 要求）
	volume = strings.TrimRight(volume, `\/`) + `\`
	return volume, nil
}
