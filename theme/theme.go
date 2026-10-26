// Package theme 管理应用外观模式：跟随系统 / 强制浅色 / 强制暗黑。
package theme

import (
	"fmt"
	"log"

	"my-go-app/store"
)

// Mode 是明暗模式。取值与 mygo.ThemeSource 一一对应，
// 生产适配器可直接转型（见 main.go），无需映射表。
type Mode string

// 三档外观模式。
const (
	ModeSystem Mode = "system" // 跟随系统（默认，也是非法配置的兜底值）
	ModeLight  Mode = "light"  // 强制浅色
	ModeDark   Mode = "dark"   // 强制暗黑
)

// SettingsKey 是主题偏好在 store.settings 表中的键（导出供工具/测试引用）。
const SettingsKey = "theme.mode"

// ParseMode 解析模式值；空串/未知值回退 ModeSystem。
// 这是配置容错入口：库被外部改坏时应用仍能正常以"跟随系统"启动。
func ParseMode(s string) Mode {
	switch Mode(s) {
	case ModeLight, ModeDark:
		return Mode(s)
	default:
		return ModeSystem
	}
}

// Index 返回模式在 UI 切换器（Auto/Light/Dark）中的序号。
func (m Mode) Index() int {
	switch m {
	case ModeLight:
		return 1
	case ModeDark:
		return 2
	default:
		return 0
	}
}

// ModeOfIndex 是 Index 的逆映射；越界回退 ModeSystem（防御非法输入）。
func ModeOfIndex(i int) Mode {
	switch i {
	case 1:
		return ModeLight
	case 2:
		return ModeDark
	default:
		return ModeSystem
	}
}

// Manager 在 UI 主线程加载、切换并保存外观模式。
type Manager struct {
	mode  Mode
	apply func(Mode)
	store *store.Store
}

// New 创建 Manager：读取已存偏好（缺省/非法回退 ModeSystem）并立即应用。
//
// st 和 apply 必须由应用初始化时提供。
//
// 启动即 Apply 的原因：SetSource 支持在窗口创建前调用，
// 让第一个窗口直接以正确外观呈现。
func New(st *store.Store, apply func(Mode)) *Manager {
	m := &Manager{mode: ModeSystem, apply: apply, store: st}
	v, _, err := st.GetSetting(SettingsKey)
	if err != nil {
		log.Printf("theme: 读取主题偏好失败（按跟随系统继续）: %v", err)
	} else {
		m.mode = ParseMode(v)
	}
	m.apply(m.mode)
	return m
}

// Mode 返回当前外观模式。
func (m *Manager) Mode() Mode { return m.mode }

// SetMode 切换外观模式：先立即生效，再持久化。
// mode 使用 ModeSystem、ModeLight 或 ModeDark；存储值由 New 解析。
//
// 错误语义：持久化失败不影响本次切换（外观已生效），
// 仅导致"重启后不记得该偏好"，错误上抛由调用方提示。
func (m *Manager) SetMode(mode Mode) error {
	if mode == m.mode {
		return nil
	}
	m.mode = mode
	m.apply(mode)
	if err := m.store.SetSetting(SettingsKey, string(mode)); err != nil {
		return fmt.Errorf("theme: 保存主题偏好失败: %w", err)
	}
	return nil
}
