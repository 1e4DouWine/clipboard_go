//go:build windows

package watch

import (
	"syscall"
	"unsafe"
)

// Win32 依赖（标准库 syscall，零新增第三方依赖）。
var (
	user32                  = syscall.NewLazyDLL("user32.dll")
	procGetForegroundWindow = user32.NewProc("GetForegroundWindow")
	procGetWindowTextW      = user32.NewProc("GetWindowTextW")
)

// foregroundTitle 返回当前前台窗口的标题（Windows 实现）。
//
// 用途：① 敏感窗口过滤；② 记录条目来源应用。
//
// 边界处理：
//   - 无前台窗口（桌面/启动器）→ 返回 ""，由调用方按"无来源"处理；
//   - API 调用失败或标题读取失败 → 返回 ""，绝不 panic（过滤逻辑会放行）；
//   - 标题超长时按缓冲区截断，Win32 侧保证不越界。
func foregroundTitle() string {
	hwnd, _, _ := procGetForegroundWindow.Call()
	if hwnd == 0 {
		return "" // 没有前台窗口（例如锁屏/桌面）
	}

	// GetWindowTextW 需要 UTF-16 缓冲区；512 字符覆盖绝大多数窗口标题。
	const maxLen = 512
	buf := make([]uint16, maxLen)
	n, _, _ := procGetWindowTextW.Call(
		hwnd,
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(maxLen),
	)
	if n == 0 {
		return "" // 无标题窗口或跨进程读取失败（受 UIPI 限制），静默降级
	}
	// n 为写入的字符数（不含终止符），按实际长度转换避免残留缓冲区内容混入。
	return syscall.UTF16ToString(buf[:n])
}
