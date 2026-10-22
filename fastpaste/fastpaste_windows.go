//go:build windows

// Package fastpaste 提供"快速粘贴"能力：把选中的历史条目写回剪贴板后，
// 自动回填到用户之前所在的窗口（模拟按下 Ctrl+V）。
//
// Windows 实现：Win32 GetForegroundWindow/SetForegroundWindow/SendInput。
// 仅使用标准库 syscall，不引入第三方依赖。
//
// 安全边界：注入可能被目标应用拒绝（UAC 提权窗口、全屏游戏等），
// 调用方必须处理 Paste 返回的错误（降级为"仅写回剪贴板"）。
package fastpaste

import (
	"errors"
	"syscall"
	"time"
	"unsafe"
)

// Win32 常量（winuser.h）。
const (
	inputKeyboard    = 1                     // KEYBDINPUT 类型
	keyeventfKeyup   = 2                     // 按键抬起
	vkControl        = 0x11                  // VK_CONTROL
	vkV              = 0x56                  // 字母 V 的虚拟键码
	pasteSettleDelay = 30 * time.Millisecond // 激活目标窗口到注入按键的等待
)

// Target 是"快速粘贴目标窗口"的句柄包装。
// 零值表示没有有效目标（此时 Paste 直接返回 ErrNoTarget）。
type Target struct {
	hwnd uintptr // Win32 HWND；0 表示无效
}

// Valid 报告目标窗口句柄是否有效。
func (t Target) Valid() bool { return t.hwnd != 0 }

// 错误语义。
var (
	// ErrNoTarget 表示没有可粘贴的目标窗口（例如窗口已关闭）。
	ErrNoTarget = errors.New("fastpaste: 没有可用的目标窗口")
)

// Win32 API 懒加载。
var (
	user32                  = syscall.NewLazyDLL("user32.dll")
	procGetForegroundWindow = user32.NewProc("GetForegroundWindow")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procIsWindow            = user32.NewProc("IsWindow")
	procSendInput           = user32.NewProc("SendInput")
)

// keyboardInput 对应 Win32 KEYBDINPUT 结构。
type keyboardInput struct {
	wVk         uint16
	wScan       uint16
	dwFlags     uint32
	time        uint32
	dwExtraInfo uintptr
}

// input 对应 Win32 INPUT 结构（仅键盘分支）。
// 字段顺序与对齐必须与 C 结构一致：type(4) + 隐式对齐(4) + KEYBDINPUT(24) = 32 字节。
type input struct {
	typ uint32
	_   uint32 // 占位：union 起始对齐到 8 字节
	ki  keyboardInput
}

// Capture 记录当前前台窗口作为粘贴目标。
//
// 调用时机很重要：必须在弹出本应用窗口之前调用，
// 此刻前台窗口仍是用户的原目标窗口（浏览器、编辑器等）。
// 无前台窗口（桌面/锁屏）时返回零值 Target。
func Capture() Target {
	hwnd, _, _ := procGetForegroundWindow.Call()
	return Target{hwnd: hwnd}
}

// Paste 激活目标窗口并模拟按下 Ctrl+V。
//
// 失败路径（均返回 error，由调用方降级为"仅写回剪贴板"）：
//   - 目标无效或窗口已销毁 → ErrNoTarget；
//   - 激活失败（前台权限不足等）→ 包装错误；
//   - 按键注入被拒绝 → 包装错误。
func Paste(t Target) error {
	// 边界：目标无效（从未记录或捕获失败）。
	if !t.Valid() {
		return ErrNoTarget
	}
	// 边界：目标窗口已关闭（句柄失效），IsWindow 判定。
	if ok, _, _ := procIsWindow.Call(t.hwnd); ok == 0 {
		return ErrNoTarget
	}

	// 激活目标窗口。SetForegroundWindow 可能被系统限制（前台锁定），
	// 失败时返回 0：继续注入没有意义，直接报错让调用方降级。
	if ok, _, _ := procSetForegroundWindow.Call(t.hwnd); ok == 0 {
		return errors.New("fastpaste: 激活目标窗口失败（可能被系统限制）")
	}

	// 等待目标窗口真正获得焦点，否则按键可能仍落到原焦点上。
	time.Sleep(pasteSettleDelay)

	// 注入 Ctrl+V：Ctrl 按下 → V 按下 → V 抬起 → Ctrl 抬起。
	inputs := [4]input{
		{typ: inputKeyboard, ki: keyboardInput{wVk: vkControl}},
		{typ: inputKeyboard, ki: keyboardInput{wVk: vkV}},
		{typ: inputKeyboard, ki: keyboardInput{wVk: vkV, dwFlags: keyeventfKeyup}},
		{typ: inputKeyboard, ki: keyboardInput{wVk: vkControl, dwFlags: keyeventfKeyup}},
	}
	sent, _, callErr := procSendInput.Call(
		uintptr(len(inputs)),
		uintptr(unsafe.Pointer(&inputs[0])),
		uintptr(unsafe.Sizeof(inputs[0])),
	)
	// SendInput 返回实际注入的事件数；少于预期说明部分/全部被拒绝
	//（UIPI 会阻止向更高完整性级别的窗口注入）。
	if sent != uintptr(len(inputs)) {
		return errors.New("fastpaste: 按键注入被目标拒绝（UIPI/安全限制）: " + callErr.Error())
	}
	return nil
}
