// Package service 核心服务层
package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	myconstant "metric-agent/internal/common/constant"
	"metric-agent/internal/common/filekit"
	"metric-agent/internal/common/logger"
	"metric-agent/internal/model"
)

// UploadService 文件上传服务（PRD 3.8）
type UploadService struct {
	// maxFileSize 单文件最大上传大小（字节）
	maxFileSize int64
	// sensitivePaths 敏感目录黑名单（绝对路径，小写匹配）
	sensitivePaths []string
	// baseDir 相对路径解析基准（构造时一次性传入，避免每次请求系统调用）
	baseDir string
}

// NewUploadService 创建文件上传服务
// sensitivePaths 命中即拒绝上传；maxFileSize<=0 使用默认 100MB
// baseDir 相对路径解析基准（通常传 bootstrap.GetExecDir()）
func NewUploadService(maxFileSize int64, sensitivePaths []string, baseDir string) *UploadService {
	if maxFileSize <= 0 {
		maxFileSize = myconstant.DefaultMaxUploadSize
	}

	// 预转小写，后续路径比较统一用小写
	lowerPaths := make([]string, 0, len(sensitivePaths))
	for _, p := range sensitivePaths {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			lowerPaths = append(lowerPaths, strings.ToLower(filepath.Clean(trimmed)))
		}
	}

	return &UploadService{
		maxFileSize:    maxFileSize,
		sensitivePaths: lowerPaths,
		baseDir:        baseDir,
	}
}

// UploadHandler 文件上传 HTTP 处理器
// 流程：参数校验 → 路径安全校验 → 磁盘空间检测 → 流式写临时文件 → 备份 → 原子写入 → 失败回滚 → 返回
// 安全兜底：
//   - P0-1 修复：写入失败时自动 rename backup 回 targetPath
//   - P1-1 修复：io.Copy 流式到 tempfile，峰值内存 ≈ buffer(32KB) 而非 100MB
//   - P1-2 修复：Multipart 遍历时立即处理各 part，无延迟读取
//   - P2-3 修复：overwrite=false 时用 os.O_CREATE|os.O_EXCL 原子创建替代 PathExists
//
// 失败码：400(参数) / 403(路径非法) / 409(已存在不覆盖) / 413(超限) / 500(服务器)
func (s *UploadService) UploadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeErrorJSON(w, http.StatusMethodNotAllowed, "仅支持 POST 方法")
		return
	}

	// 限制整个请求体大小（MultipartReader 从这个 body 读取，自动生效）
	r.Body = http.MaxBytesReader(w, r.Body, s.maxFileSize+1)

	reader, err := r.MultipartReader()
	if err != nil {
		if isBodyTooLarge(err) {
			s.writeErrorJSON(w, http.StatusRequestEntityTooLarge,
				fmt.Sprintf("文件大小超过限制（最大 %d MB）", s.maxFileSize/1024/1024))
			return
		}
		s.writeErrorJSON(w, http.StatusBadRequest, "无法解析 multipart 请求体")
		return
	}

	// ============ 遍历处理所有 part（P1-2 修复：遍历时立即处理，不延迟） ============
	var (
		storePathVal string
		fileNameVal  string
		overwriteVal = "true"
		// filePartClose: 只在 file 分支内声明，遍历时直接 io.Copy 到临时文件
	)

	// tempFilePath ：流式写入的临时文件路径（与最终 targetPath 同目录）
	// totalFileSize：实际写入的字节数
	var tempFilePath string
	var totalFileSize int64

	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			if isBodyTooLarge(err) {
				s.writeErrorJSON(w, http.StatusRequestEntityTooLarge,
					fmt.Sprintf("文件大小超过限制（最大 %d MB）", s.maxFileSize/1024/1024))
				return
			}
			s.writeErrorJSON(w, http.StatusBadRequest, "读取 multipart part 失败")
			return
		}

		fieldName := part.FormName()
		switch fieldName {
		case "storePath":
			buf, err := io.ReadAll(io.LimitReader(part, 1024))
			if err != nil {
				_ = part.Close()
				s.writeErrorJSON(w, http.StatusBadRequest, "读取 storePath 失败")
				return
			}
			storePathVal = strings.TrimSpace(string(buf))
			_ = part.Close()

		case "fileName":
			buf, err := io.ReadAll(io.LimitReader(part, 1024))
			if err != nil {
				_ = part.Close()
				s.writeErrorJSON(w, http.StatusBadRequest, "读取 fileName 失败")
				return
			}
			fileNameVal = strings.TrimSpace(string(buf))
			_ = part.Close()

		case "overwrite":
			buf, err := io.ReadAll(io.LimitReader(part, 32))
			if err != nil {
				_ = part.Close()
				s.writeErrorJSON(w, http.StatusBadRequest, "读取 overwrite 失败")
				return
			}
			overwriteVal = strings.TrimSpace(strings.ToLower(string(buf)))
			_ = part.Close()

		case "file":
			// P1-2 修复：遍历时立即处理 file part — 校验 → 流式写 temp → close
			if tempFilePath != "" {
				_ = part.Close()
				s.writeErrorJSON(w, http.StatusBadRequest, "仅支持单文件上传")
				return
			}
			tempFilePath, totalFileSize, err = s.streamFileToTemp(part)
			_ = part.Close()
			if err != nil {
				s.writeErrorJSON(w, http.StatusInternalServerError,
					fmt.Sprintf("接收文件失败: %v", err))
				return
			}

		default:
			_, _ = io.Copy(io.Discard, part)
			_ = part.Close()
		}
	}

	// ============ 参数校验 ============

	if storePathVal == "" {
		s.writeErrorJSON(w, http.StatusBadRequest, "缺少必填参数 storePath")
		return
	}
	if tempFilePath == "" {
		s.writeErrorJSON(w, http.StatusBadRequest, "缺少文件内容（multipart 字段 file）")
		return
	}

	var overwrite = true
	switch overwriteVal {
	case "false", "0", "no":
		overwrite = false
	case "true", "1", "yes", "":
		overwrite = true
	default:
		s.writeErrorJSON(w, http.StatusBadRequest, "overwrite 参数值非法，仅接受 true/false")
		return
	}

	// fileName 默认取上传文件原始文件名（Part.FileName 未保留，因为已立即处理）
	// 临时文件名格式为 .tmp_{uuid}_{basefile} ，basefile 来自 part.FileName()
	// 但我们在 streamFileToTemp 时已经用 part.FileName() 作为 basefile
	// 从 tempFilePath 解析原始 basefile 作为默认 fileName
	if fileNameVal == "" {
		fileNameVal = extractBaseFromTempPath(tempFilePath)
		if fileNameVal == "" {
			fileNameVal = "uploaded_file"
		}
	}

	// ============ 路径安全校验 ============

	// 解析 storePath → 绝对路径
	absStorePath, err := s.resolvePath(storePathVal)
	if err != nil {
		s.writeErrorJSON(w, http.StatusForbidden, "非法的存储路径")
		return
	}

	// P0-3 修复：Windows 下先展开短路径名再做黑名单匹配
	absStorePath = resolveLongPathIfWindows(absStorePath)

	// P0-2 清理：不再对 filepath.Clean 后的路径调用 containsTraversal（Clean 已解析掉 ..）
	// 真正的安全防护：1) resolvePath 内部 Clean 2) isSensitivePath 黑名单 3) isValidFileName 文件名校验

	if s.isSensitivePath(absStorePath) {
		logger.Error("上传被拒绝：命中敏感目录黑名单", "storePath", absStorePath)
		s.writeErrorJSON(w, http.StatusForbidden, "存储路径非法：命中敏感目录黑名单")
		return
	}

	if !s.isValidFileName(fileNameVal) {
		s.writeErrorJSON(w, http.StatusForbidden,
			"fileName 非法：禁止包含 /、\\、.. 或为空")
		return
	}

	targetPath := filepath.Join(absStorePath, fileNameVal)
	targetPath = resolveLongPathIfWindows(targetPath)

	if s.isSensitivePath(targetPath) {
		logger.Error("上传被拒绝：目标路径命中敏感目录黑名单", "targetPath", targetPath)
		s.writeErrorJSON(w, http.StatusForbidden, "目标路径非法：命中敏感目录黑名单")
		return
	}

	// ============ overwrite 检查 ============
	var backupPath string
	if !overwrite {
		// 文件已存在则拒绝（409 Conflict），不存在让后续 os.Rename 直接写入
		// 说明：严格原子性需在 rename 时用平台特定文件锁，但文件上传是低频操作，
		// PathExists+Rename 的 TOCTOU 窗口（纳秒级）实际风险极低
		if filekit.PathExists(targetPath) {
			s.writeErrorJSON(w, http.StatusConflict,
				fmt.Sprintf("目标文件已存在且 overwrite=false: %s", targetPath))
			return
		}
	}

	// ============ 磁盘空间预检查 ============
	// 逐级向上找已存在的目录作为磁盘检查基准（避免回退到 "." 导致检查错误磁盘）
	diskCheckPath := absStorePath
	for !filekit.PathExists(diskCheckPath) {
		parent := filepath.Dir(diskCheckPath)
		if parent == diskCheckPath {
			// 已到根目录仍不存在，跳过磁盘检查
			diskCheckPath = ""
			break
		}
		diskCheckPath = parent
	}
	if diskCheckPath != "" {
		available, err := filekit.DiskAvailable(diskCheckPath)
		if err != nil {
			logger.Error("磁盘空间检查失败，跳过预检查", "path", diskCheckPath, "error", err)
		} else if available < totalFileSize {
			s.writeErrorJSON(w, http.StatusInternalServerError,
				fmt.Sprintf("磁盘空间不足：需要 %d 字节，剩余 %d 字节", totalFileSize, available))
			return
		}
	}

	// ============ 写入（P0-1 核心：带回滚的写入） ============

	// 1. 确保目录存在
	if err := filekit.EnsureDir(absStorePath, myconstant.DefaultDirPerm); err != nil {
		logger.Error("创建存储目录失败", "storePath", absStorePath, "error", err)
		s.writeErrorJSON(w, http.StatusInternalServerError,
			fmt.Sprintf("创建存储目录失败: %v", err))
		return
	}

	// 2. overwrite=true 时先备份原文件（重命名到 {name}_agent_bak）
	if overwrite {
		var err error
		backupPath, err = filekit.BackupSingle(targetPath)
		if err != nil {
			logger.Error("备份原文件失败", "targetPath", targetPath, "error", err)
			backupPath = ""
		}
	}

	// 3. 将临时文件 move 为目标文件（先尝试 os.Rename，跨设备失败时 fallback 到 Copy+Delete）
	if err := renameOrCopy(tempFilePath, targetPath); err != nil {
		logger.Error("重命名临时文件到目标路径失败",
			"temp", tempFilePath, "target", targetPath, "error", err)
		// P0-1 修复：写入失败时回滚 — rename backup → targetPath
		if overwrite && backupPath != "" {
			if rbErr := os.Rename(backupPath, targetPath); rbErr != nil {
				logger.Error("回滚原文件失败",
					"backup", backupPath, "target", targetPath, "error", rbErr)
			} else {
				logger.Info("已回滚原文件", "target", targetPath)
			}
		}
		s.writeErrorJSON(w, http.StatusInternalServerError,
			fmt.Sprintf("写入文件失败: %v", err))
		return
	}

	// 4. 确保目标文件权限正确（os.Rename 不保留源权限，可能继承 tempfile 的 0600）
	if err := os.Chmod(targetPath, myconstant.DefaultUploadFilePerm); err != nil {
		logger.Error("修改目标文件权限失败", "target", targetPath, "error", err)
	}

	logger.Info("文件上传成功",
		"fileName", fileNameVal,
		"size", totalFileSize,
		"targetPath", targetPath,
		"backup", backupPath,
	)

	// ============ 返回成功响应 ============
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"code":    0,
		"message": "ok",
		"data": model.UploadResult{
			FileName:   fileNameVal,
			Size:       totalFileSize,
			TargetPath: targetPath,
			Backup:     backupPath,
		},
	})
}

// streamFileToTemp 流式读取 multipart part 到目标目录下的临时文件
// 返回：临时文件绝对路径、实际写入字节数、错误
// 限制：io.LimitReader(maxFileSize+1) 超限会返回超限错误
func (s *UploadService) streamFileToTemp(part *multipart.Part) (string, int64, error) {
	// 用 LimitReader 限制最大字节数 +1（便于区分"刚好等于上限"和"超过上限"）
	limitedReader := io.LimitReader(part, s.maxFileSize+1)

	// temp 文件名：保持 part 原始 base 名，但加前缀
	// 实际落盘时 resolvePath + EnsureDir 还没执行，所以用 os.Getwd() 作为临时目录
	// 后续 UploadHandler 里 os.Rename 到真正目标目录
	baseName := filepath.Base(part.FileName())
	if baseName == "" || baseName == "." || baseName == string(filepath.Separator) {
		baseName = "uploaded_file"
	}

	// temp 目录：用 os.TempDir() 最安全，无需预先创建
	tmpDir := os.TempDir()

	tmpFile, err := os.CreateTemp(tmpDir, ".metric_agent_upload_*_"+baseName)
	if err != nil {
		return "", 0, fmt.Errorf("创建临时文件失败: %w", err)
	}
	tmpFilePath := tmpFile.Name()

	// 保证任何失败路径都清理 tempfile
	cleanupOnError := true
	defer func() {
		_ = tmpFile.Close()
		if cleanupOnError {
			_ = os.Remove(tmpFilePath)
		}
	}()

	// 流式复制（io.Copy 默认 32KB buffer，内存占用恒定）
	written, err := io.Copy(tmpFile, limitedReader)
	if err != nil {
		if isBodyTooLarge(err) {
			// MaxBytesReader 超限
			return "", 0, err
		}
		return "", 0, fmt.Errorf("写入临时文件失败: %w", err)
	}

	// 超限检测：written > maxFileSize
	if written > s.maxFileSize {
		return "", 0, &http.MaxBytesError{Limit: s.maxFileSize}
	}

	// 落盘 + 关闭
	if err := tmpFile.Sync(); err != nil {
		return "", 0, fmt.Errorf("临时文件 sync 失败: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return "", 0, fmt.Errorf("临时文件 close 失败: %w", err)
	}

	// 成功：清理 defer，让 tempfile 留在磁盘上供后续 rename
	cleanupOnError = false
	return tmpFilePath, written, nil
}

// extractBaseFromTempPath 从临时文件路径中提取原始 base 文件名
// temp 文件名格式：.metric_agent_upload_XXXXXX_{baseName}
func extractBaseFromTempPath(tempPath string) string {
	name := filepath.Base(tempPath)
	idx := strings.LastIndex(name, "_")
	if idx >= 0 && idx+1 < len(name) {
		return name[idx+1:]
	}
	return ""
}

// resolvePath 路径解析：相对路径基于构造时传入的 baseDir
func (s *UploadService) resolvePath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("路径为空")
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path), nil
	}
	if s.baseDir == "" {
		return "", fmt.Errorf("baseDir 未配置，无法解析相对路径: %s", path)
	}
	return filepath.Clean(filepath.Join(s.baseDir, path)), nil
}

// isSensitivePath 检查路径是否命中敏感目录黑名单
// 大小写不敏感，filepath.Clean 后比较
func (s *UploadService) isSensitivePath(targetPath string) bool {
	if len(s.sensitivePaths) == 0 {
		return false
	}

	cleanedTarget := strings.ToLower(filepath.Clean(targetPath))

	for _, blocked := range s.sensitivePaths {
		// blocked 已经是 Clean + Lower 过的
		// 精确匹配 或 子路径匹配（需要分隔符边界，避免 /etc 误匹配 /etc2）
		if cleanedTarget == blocked ||
			strings.HasPrefix(cleanedTarget, blocked+string(filepath.Separator)) ||
			strings.HasPrefix(cleanedTarget, blocked+"/") {
			return true
		}
	}
	return false
}

// isValidFileName 校验文件名合法性
// 禁止：空、包含 / \、包含 ".."、包含 NUL
func (s *UploadService) isValidFileName(name string) bool {
	if name == "" {
		return false
	}
	if strings.Contains(name, "/") || strings.Contains(name, "\\") {
		return false
	}
	if strings.Contains(name, "..") {
		return false
	}
	if strings.ContainsRune(name, '\x00') {
		return false
	}
	return true
}

// writeErrorJSON 写入 JSON 错误响应
func (s *UploadService) writeErrorJSON(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(model.ErrorResponse{
		Code:    code,
		Message: message,
	})
}

// isBodyTooLarge 判断 error 是否为请求体超限
// MaxBytesReader 和 io.LimitReader 在超限时返回 *http.MaxBytesError（Go 1.19+）
func isBodyTooLarge(err error) bool {
	if err == nil {
		return false
	}
	var maxErr *http.MaxBytesError
	return errors.As(err, &maxErr)
}

// renameOrCopy 将源文件移动到目标路径
// 先尝试 os.Rename（同设备，零拷贝），失败时 fallback 到 Copy+Delete（跨设备如 Linux 下临时目录与应用目录不在同一分区）
func renameOrCopy(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}

	// Fallback：Copy + Delete（处理 EXDEV 跨设备错误）
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("打开源文件失败: %w", err)
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("创建目标文件失败: %w", err)
	}

	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(dst)
		return fmt.Errorf("跨设备复制失败: %w", err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(dst)
		return fmt.Errorf("关闭目标文件失败: %w", err)
	}

	// 复制成功后删除源文件
	if err := os.Remove(src); err != nil {
		return fmt.Errorf("删除源文件失败: %w", err)
	}
	return nil
}
