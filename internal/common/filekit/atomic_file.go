// Package filekit 文件操作工具集
package filekit

import (
	"fmt"
	"os"
)

// PathExists 判断文件或目录是否存在
func PathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// EnsureDir 确保目录存在，不存在则创建
func EnsureDir(dirPath string, perm os.FileMode) error {
	if PathExists(dirPath) {
		// 已存在，检查是否为目录
		info, err := os.Stat(dirPath)
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return fmt.Errorf("路径已存在但不是目录: %s", dirPath)
		}
		return nil
	}

	// 创建目录
	return os.MkdirAll(dirPath, perm)
}
