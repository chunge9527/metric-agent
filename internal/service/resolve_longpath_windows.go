//go:build windows

package service

import (
	"syscall"
	"unsafe"
)

// resolveLongPathIfWindows 将可能包含 8.3 短路径名的路径展开为完整长路径
// Windows API: kernel32.GetLongPathNameW
// 例如: C:\PROGRA~1 → C:\Program Files
// 如果路径不存在，API 会原样返回；失败也原样返回（不阻断流程，只是黑名单匹配可能无效）
func resolveLongPathIfWindows(path string) string {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	getLongPathName := kernel32.NewProc("GetLongPathNameW")

	pathPtr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return path
	}

	// 第一次调用：查询所需 buffer 大小
	bufSize, _, callErr := getLongPathName.Call(
		uintptr(unsafe.Pointer(pathPtr)),
		0,
		0,
	)
	if callErr != nil || bufSize == 0 {
		return path
	}

	buf := make([]uint16, bufSize)
	getLongPathName.Call(
		uintptr(unsafe.Pointer(pathPtr)),
		uintptr(unsafe.Pointer(&buf[0])),
		bufSize,
	)

	result := syscall.UTF16ToString(buf)
	if result == "" {
		return path
	}
	return result
}
