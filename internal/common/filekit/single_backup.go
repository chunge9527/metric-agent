// Package filekit 文件操作工具集
package filekit

import (
	"fmt"
	"os"
	"path/filepath"

	"metric-agent/internal/common/constant"
)

// BackupSingle 单版本备份（上传专用，PRD 3.8）
// 与 BackupFile 行为一致（都是单版本 _agent_bak），区别在于：
//   - 返回备份文件路径（调用方需要用它做回滚）
//   - 不加包级互斥锁（上传场景并发安全由调用方保证）
//   - 目标文件不存在则跳过，返回空字符串（表示未备份）
//
// 返回：备份文件路径（空字符串表示原文件不存在，未执行备份）
func BackupSingle(filePath string) (string, error) {
	if !PathExists(filePath) {
		return "", nil
	}

	dir := filepath.Dir(filePath)
	baseName := filepath.Base(filePath)
	backupPath := filepath.Join(dir, baseName+constant.BackupFileSuffix)

	// 直接覆盖旧备份：先 Remove 再 Rename
	_ = os.Remove(backupPath)
	if err := os.Rename(filePath, backupPath); err != nil {
		return "", fmt.Errorf("单版本备份失败 %s → %s: %w", filePath, backupPath, err)
	}

	return backupPath, nil
}
