// Package errors 统一错误类型定义
package errors

import "fmt"

// ErrorCode 错误码类型
type ErrorCode int

const (
	// ErrCodeInvalidParam 参数错误
	ErrCodeInvalidParam ErrorCode = 40001
	// ErrCodeMissingParam 缺少必填参数
	ErrCodeMissingParam ErrorCode = 40002
	// ErrCodeInvalidFormat 格式错误
	ErrCodeInvalidFormat ErrorCode = 40003

	// ErrCodeConfigNotFound 配置文件不存在
	ErrCodeConfigNotFound ErrorCode = 41001
	// ErrCodeConfigParse 配置解析失败
	ErrCodeConfigParse ErrorCode = 41002
	// ErrCodeConfigValidate 配置校验失败
	ErrCodeConfigValidate ErrorCode = 41003

	// ErrCodeNetworkError 网络错误
	ErrCodeNetworkError ErrorCode = 42001
	// ErrCodeNacosConnect Nacos连接失败
	ErrCodeNacosConnect ErrorCode = 42002
	// ErrCodeNacosPull Nacos配置拉取失败
	ErrCodeNacosPull ErrorCode = 42003
	// ErrCodeForwardInvalid 透传目标地址非法
	ErrCodeForwardInvalid ErrorCode = 42005
	// ErrCodeForwardTimeout 透传请求超时
	ErrCodeForwardTimeout ErrorCode = 42006
	// ErrCodeForwardUnreachable 透传目标不可达
	ErrCodeForwardUnreachable ErrorCode = 42007

	// ErrCodeExecFailed 脚本执行失败
	ErrCodeExecFailed ErrorCode = 43001
	// ErrCodeExecTimeout 脚本执行超时
	ErrCodeExecTimeout ErrorCode = 43002
	// ErrCodeEncrypt 加密失败
	ErrCodeEncrypt ErrorCode = 43003
	// ErrCodeDecrypt 解密失败
	ErrCodeDecrypt ErrorCode = 43004
	// ErrCodeShellExec Shell执行错误
	ErrCodeShellExec ErrorCode = 43005

	// ErrCodeFileWrite 文件写入失败
	ErrCodeFileWrite ErrorCode = 44001
	// ErrCodeFileRead 文件读取失败
	ErrCodeFileRead ErrorCode = 44002
	// ErrCodeFileBackup 文件备份失败
	ErrCodeFileBackup ErrorCode = 44003
	// ErrCodeFileRestore 文件恢复失败
	ErrCodeFileRestore ErrorCode = 44004
	// ErrCodeFileRename 文件重命名失败
	ErrCodeFileRename ErrorCode = 44005

	// ErrCodeAuthFail 鉴权失败
	ErrCodeAuthFail ErrorCode = 45001

	// ErrCodeInternal 内部错误
	ErrCodeInternal ErrorCode = 50001
)

// AppError 统一应用错误结构
type AppError struct {
	Code    ErrorCode `json:"code"`
	Message string    `json:"message"`
	Err     error     `json:"-"`
}

// Error 实现error接口
func (e *AppError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("[%d] %s: %v", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("[%d] %s", e.Code, e.Message)
}

// Unwrap 支持errors.Unwrap
func (e *AppError) Unwrap() error {
	return e.Err
}

// Is 支持errors.Is，按错误码匹配
func (e *AppError) Is(target error) bool {
	if t, ok := target.(*AppError); ok {
		return e.Code == t.Code
	}
	return false
}

// New 创建新的应用错误
func New(code ErrorCode, message string) *AppError {
	return &AppError{
		Code:    code,
		Message: message,
	}
}

// Wrap 包装已有错误
func Wrap(code ErrorCode, message string, err error) *AppError {
	return &AppError{
		Code:    code,
		Message: message,
		Err:     err,
	}
}

// ============ 预定义便捷错误 ============

// ErrInvalidParam 参数错误
func ErrInvalidParam(msg string) *AppError {
	return New(ErrCodeInvalidParam, msg)
}

// ErrMissingParam 缺少必填参数
func ErrMissingParam(param string) *AppError {
	return New(ErrCodeMissingParam, fmt.Sprintf("缺少必填参数: %s", param))
}

// ErrInvalidFormat 格式错误
func ErrInvalidFormat(msg string) *AppError {
	return New(ErrCodeInvalidFormat, msg)
}

// ErrConfigNotFound 配置文件不存在
func ErrConfigNotFound(path string) *AppError {
	return New(ErrCodeConfigNotFound, fmt.Sprintf("配置文件不存在: %s", path))
}

// ErrConfigParse 配置解析失败
func ErrConfigParse(msg string) *AppError {
	return New(ErrCodeConfigParse, msg)
}

// ErrConfigValidate 配置校验失败
func ErrConfigValidate(msg string) *AppError {
	return New(ErrCodeConfigValidate, msg)
}

// ErrNacosConnect Nacos连接失败
func ErrNacosConnect(err error) *AppError {
	return Wrap(ErrCodeNacosConnect, "Nacos连接失败", err)
}

// ErrNacosPull Nacos配置拉取失败
func ErrNacosPull(dataID string, err error) *AppError {
	return Wrap(ErrCodeNacosPull, fmt.Sprintf("Nacos配置拉取失败: %s", dataID), err)
}

// ErrForwardInvalid 透传目标地址非法
func ErrForwardInvalid(url string) *AppError {
	return New(ErrCodeForwardInvalid, fmt.Sprintf("透传目标地址非法: %s", url))
}

// ErrForwardTimeout 透传请求超时
func ErrForwardTimeout(url string) *AppError {
	return New(ErrCodeForwardTimeout, fmt.Sprintf("透传请求超时: %s", url))
}

// ErrForwardUnreachable 透传目标不可达
func ErrForwardUnreachable(url string, err error) *AppError {
	return Wrap(ErrCodeForwardUnreachable, fmt.Sprintf("透传目标不可达: %s", url), err)
}

// ErrExecFailed 脚本执行失败
func ErrExecFailed(msg string, err error) *AppError {
	return Wrap(ErrCodeExecFailed, msg, err)
}

// ErrExecTimeout 脚本执行超时
func ErrExecTimeout(script string) *AppError {
	return New(ErrCodeExecTimeout, fmt.Sprintf("脚本执行超时: %s", script))
}

// ErrEncrypt 加密失败
func ErrEncrypt(err error) *AppError {
	return Wrap(ErrCodeEncrypt, "加密失败", err)
}

// ErrDecrypt 解密失败
func ErrDecrypt(err error) *AppError {
	return Wrap(ErrCodeDecrypt, "解密失败", err)
}

// ErrFileWrite 文件写入失败
func ErrFileWrite(path string, err error) *AppError {
	return Wrap(ErrCodeFileWrite, fmt.Sprintf("文件写入失败: %s", path), err)
}

// ErrFileRead 文件读取失败
func ErrFileRead(path string, err error) *AppError {
	return Wrap(ErrCodeFileRead, fmt.Sprintf("文件读取失败: %s", path), err)
}

// ErrFileBackup 文件备份失败
func ErrFileBackup(path string, err error) *AppError {
	return Wrap(ErrCodeFileBackup, fmt.Sprintf("文件备份失败: %s", path), err)
}

// ErrFileRename 文件重命名失败
func ErrFileRename(path string, err error) *AppError {
	return Wrap(ErrCodeFileRename, fmt.Sprintf("文件重命名失败: %s", path), err)
}

// ErrFileRestore 文件恢复失败
func ErrFileRestore(path string, err error) *AppError {
	return Wrap(ErrCodeFileRestore, fmt.Sprintf("文件恢复失败: %s", path), err)
}

// ErrAuthFail 鉴权失败
func ErrAuthFail() *AppError {
	return New(ErrCodeAuthFail, "鉴权失败：无效的凭证")
}

// ErrInternal 内部错误
func ErrInternal(msg string, err error) *AppError {
	return Wrap(ErrCodeInternal, msg, err)
}
