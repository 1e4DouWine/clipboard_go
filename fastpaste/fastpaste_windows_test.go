//go:build windows

package fastpaste

import (
	"testing"
	"unsafe"
)

// TestInputStructMatchesWin32Layout 校验 INPUT 结构体尺寸与 Win32 ABI 一致。
//
// SendInput 要求 cbSize 恰好等于 sizeof(INPUT)：x64 为 40 字节、x86 为 28 字节。
// 尺寸不符时 SendInput 直接失败（ERROR_INVALID_PARAMETER），
// 表现为"快速粘贴永远失败"，且不会报出明显的错因——因此在这里钉死。
func TestInputStructMatchesWin32Layout(t *testing.T) {
	want := uintptr(40)
	if unsafe.Sizeof(uintptr(0)) == 4 {
		want = 28 // 32 位进程
	}
	if got := unsafe.Sizeof(input{}); got != want {
		t.Fatalf("sizeof(INPUT) = %d，期望 %d（尺寸不符会让 SendInput 直接失败）", got, want)
	}
}

// TestKeyboardInputLayout 校验 KEYBDINPUT 字段偏移与 Win32 一致。
func TestKeyboardInputLayout(t *testing.T) {
	var ki keyboardInput
	// wVk(2) + wScan(2) + dwFlags(4) + time(4) + dwExtraInfo(8/4)
	want := uintptr(24)
	if unsafe.Sizeof(uintptr(0)) == 4 {
		want = 16
	}
	if got := unsafe.Sizeof(ki); got != want {
		t.Fatalf("sizeof(KEYBDINPUT) = %d，期望 %d", got, want)
	}
}
