package fastpaste

import (
	"errors"
	"testing"
)

// TestZeroTargetIsInvalid 验证零值 Target 无效（无窗口句柄）。
func TestZeroTargetIsInvalid(t *testing.T) {
	if (Target{}).Valid() {
		t.Fatal("零值 Target 应为无效")
	}
}

// TestPasteWithoutTargetFails 验证无目标窗口时 Paste 返回 ErrNoTarget，
// 调用方据此降级为"仅写回剪贴板、提示用户手动粘贴"。
func TestPasteWithoutTargetFails(t *testing.T) {
	if err := Paste(Target{}); !errors.Is(err, ErrNoTarget) {
		t.Fatalf("Paste(零值 Target) = %v, want ErrNoTarget", err)
	}
}
