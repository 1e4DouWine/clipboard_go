//go:build !windows

// Package fastpaste 提供"快速粘贴"能力（非 Windows 平台降级实现）。
//
// macOS/Linux 暂不提供按键注入：Capture 返回零值 Target，Paste 返回 ErrNoTarget。
// 调用方的降级路径（仅写回剪贴板、提示用户手动粘贴）在此平台上生效，
// 核心功能不受影响。后续可接入 Accessibility API（macOS）或 xdotool（Linux）。
package fastpaste

import "errors"

// Target 是粘贴目标的占位类型（非 Windows 平台无句柄概念）。
type Target struct{}

// Valid 恒为 false：非 Windows 平台没有可注入的目标。
func (t Target) Valid() bool { return false }

// ErrNoTarget 表示没有可粘贴的目标窗口。
var ErrNoTarget = errors.New("fastpaste: 此平台不支持快速粘贴")

// Capture 在非 Windows 平台恒返回零值 Target。
func Capture() Target { return Target{} }

// Paste 在非 Windows 平台恒失败，调用方降级为"仅写回剪贴板"。
func Paste(t Target) error { return ErrNoTarget }
