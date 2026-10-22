package hotkey

import (
	"errors"
	"fmt"
	"strings"

	"my-go-app/store"
)

// 内置动作名（与 views 层、设置面板约定一致）。
const (
	// ActionToggleWindow 是"唤起/隐藏主窗口"动作（剪贴板工具的核心热键）。
	ActionToggleWindow = "toggle-window"
)

// settingsKeyPrefix 是快捷键在 store.settings 表中的键前缀。
const settingsKeyPrefix = "hotkey."

// DefaultAccelerators 是各动作的默认全局快捷键（未被用户自定义时使用）。
// 必须全部通过 Validate（见测试 TestDefaultAcceleratorsValid）。
var DefaultAccelerators = map[string]string{
	ActionToggleWindow: "Ctrl+Shift+V",
}

// SettingKey 返回动作在 settings 表中的存储键（如 "hotkey.toggle-window"）。
func SettingKey(action string) string { return settingsKeyPrefix + action }

// LoadAccelerator 读取用户自定义快捷键（原始存储值）。
// 第二个返回值表示是否有自定义值；空字符串视为"未自定义"。
func LoadAccelerator(s *store.Store, action string) (string, bool, error) {
	if s == nil {
		return "", false, fmt.Errorf("hotkey: store 不能为空")
	}
	v, ok, err := s.GetSetting(SettingKey(action))
	if err != nil {
		return "", false, fmt.Errorf("hotkey: 读取 %q 快捷键配置失败: %w", action, err)
	}
	if !ok || v == "" {
		return "", false, nil // 未自定义或已清空 → 回退默认值
	}
	return v, true, nil
}

// SaveAccelerator 持久化用户自定义快捷键（存规范形式，如 "Ctrl+Shift+V"）。
//
// 边界处理：
//   - acc 为空串 → 清空自定义（等价于恢复默认键）；
//   - acc 语法非法 → 拒绝写入，返回 ErrInvalidAccelerator，
//     保证配置库里永远不会出现无法解析的值。
func SaveAccelerator(s *store.Store, action, acc string) error {
	if s == nil {
		return fmt.Errorf("hotkey: store 不能为空")
	}
	if strings.TrimSpace(action) == "" {
		return fmt.Errorf("hotkey: action 不能为空")
	}
	value := ""
	if strings.TrimSpace(acc) != "" {
		canon, err := Canonical(acc) // 只做语法校验；改键入口已做过安全校验
		if err != nil {
			return err
		}
		value = canon
	}
	if err := s.SetSetting(SettingKey(action), value); err != nil {
		return fmt.Errorf("hotkey: 保存 %q 快捷键配置失败: %w", action, err)
	}
	return nil
}

// EffectiveAccelerator 解析"实际生效"的快捷键：
// 优先用户自定义值；无自定义则用默认值。
//
// 异常降级：自定义值存在但语法非法（如配置被外部改坏）时，
// 回退默认键并返回包装 ErrInvalidAccelerator 的错误——
// 返回的快捷键始终可用，调用方可凭 errors.Is 决定是否提示用户修复配置。
func EffectiveAccelerator(s *store.Store, action string) (string, error) {
	if s == nil {
		return "", fmt.Errorf("hotkey: store 不能为空")
	}
	v, ok, err := LoadAccelerator(s, action)
	if err != nil {
		return "", err
	}
	if ok {
		canon, errCanon := Canonical(v)
		if errCanon != nil {
			// 配置损坏：回退默认键，同时把问题暴露给调用方。
			def, hasDefault := DefaultAccelerators[action]
			if !hasDefault {
				return "", fmt.Errorf("%w: %q 无默认快捷键可回退", ErrUnknownAction, action)
			}
			return def, fmt.Errorf("%w: 自定义快捷键 %q 无效，已回退默认值 %q", ErrInvalidAccelerator, v, def)
		}
		return canon, nil
	}

	def, ok := DefaultAccelerators[action]
	if !ok {
		return "", fmt.Errorf("%w: %q", ErrUnknownAction, action)
	}
	return def, nil
}

// RegisterWithSettings 按"自定义优先、默认兜底"注册动作，返回实际生效的快捷键。
//
// 错误语义：
//   - 未知动作等不可恢复错误直接上抛；
//   - "自定义值无效但已回退默认键"（包装 ErrInvalidAccelerator）视为可恢复：
//     用默认键完成注册并返回该错误，调用方可提示用户修复配置；
//   - 注册冲突（ErrConflict）原样返回，不影响其他动作的注册，
//     由 UI 层提示用户改键（应用内快捷键不受影响）。
func RegisterWithSettings(m *Manager, s *store.Store, action string, fn func()) (string, error) {
	if m == nil {
		return "", fmt.Errorf("hotkey: manager 不能为空")
	}
	acc, err := EffectiveAccelerator(s, action)
	if err != nil && !errors.Is(err, ErrInvalidAccelerator) {
		return "", err // 无法确定生效键，放弃注册
	}
	if regErr := m.Register(action, acc, fn); regErr != nil {
		return "", regErr
	}
	return acc, err // err 非 nil 时为"已回退默认键"的提示型错误
}

// Change 更换 action 的快捷键并持久化，保证"配置 == 实际生效键"。
//
// 事务语义：注册与保存都成功才算成功；
// 保存失败时回滚注册（恢复原快捷键或注销），避免配置与实际不一致。
func Change(m *Manager, s *store.Store, action, acc string) error {
	if m == nil || s == nil {
		return fmt.Errorf("hotkey: manager/store 不能为空")
	}
	if _, err := normalize(acc); err != nil {
		return err // 先整体校验，失败不产生任何副作用
	}
	fn := m.handler(action)
	if fn == nil {
		return fmt.Errorf("%w: %q", ErrUnknownAction, action)
	}
	old, hadOld := m.Accelerator(action)

	if err := m.Register(action, acc, fn); err != nil {
		return err // 注册失败时 Register 内部已保持原绑定不动
	}
	if err := SaveAccelerator(s, action, acc); err != nil {
		// 持久化失败：回滚注册，保持配置与实际一致。
		if hadOld {
			_ = m.Register(action, old, fn) // 回滚失败概率极低（旧键此前一直可用）
		} else {
			m.Unregister(action)
		}
		return err
	}
	return nil
}
