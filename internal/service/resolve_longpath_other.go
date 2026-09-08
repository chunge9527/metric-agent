//go:build !windows

package service

// resolveLongPathIfWindows Unix 下空实现：直接返回原路径
// Unix 不存在 8.3 短路径名机制，无需展开
func resolveLongPathIfWindows(path string) string {
	return path
}
