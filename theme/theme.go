// Package theme 管理应用外观模式：跟随系统 / 强制浅色 / 强制暗黑。
//
// 职责：
//   - 启动时从持久化（store.settings）恢复用户偏好并立即应用，
//     避免"启动闪白再变黑"；
//   - 切换模式时立即生效（mygo.Theme.SetSource 热切换，无需重启），
//     并持久化偏好；
//   - 对上层暴露变更通知，便于托盘菜单等入口同步状态（阶段 4）。
//
// 易测性：外观应用能力抽象为 Applier 接口，生产实现包装 mygo.Theme.SetSource，
// 测试注入 fake，不依赖 mygo 应用运行环境。
package theme

import (
	"errors"
	"fmt"
	"log"
	"sync"

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
	switch ParseMode(string(m)) {
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

// Applier 是"应用外观"能力的最小依赖。
type Applier interface {
	Apply(m Mode)
}

// ApplierFunc 让普通函数满足 Applier，方便生产适配一行接入。
type ApplierFunc func(Mode)

// Apply 实现 Applier。
func (f ApplierFunc) Apply(m Mode) { f(m) }

// Manager 管理当前外观模式：加载偏好、切换、持久化、通知。
// 所有方法并发安全（内部互斥锁）。
type Manager struct {
	mu       sync.Mutex
	mode     Mode                  // 当前模式（合法值，永不为空）
	applier  Applier               // 外观应用能力
	store    *store.Store          // 可为 nil：纯内存模式（仅测试）
	nextSub  uint64                // 监听器自增 ID
	onChange map[uint64]func(Mode) // 模式变更监听（ID → 回调）
}

// New 创建 Manager：读取已存偏好（缺省/非法回退 ModeSystem）并立即应用。
//
// 参数：
//   - st：持久化存储，可为 nil（不记忆偏好）；
//   - applier：外观应用能力，为 nil 视为编程错误。
//
// 启动即 Apply 的原因：SetSource 支持在窗口创建前调用，
// 让第一个窗口直接以正确外观呈现。
func New(st *store.Store, applier Applier) (*Manager, error) {
	if applier == nil {
		return nil, errors.New("theme: Applier 不能为空")
	}
	m := &Manager{mode: ModeSystem, applier: applier, store: st, onChange: map[uint64]func(Mode){}}

	if st != nil {
		v, ok, err := st.GetSetting(SettingsKey)
		switch {
		case err != nil:
			// 读取失败不阻塞启动：按默认"跟随系统"继续。
			log.Printf("theme: 读取主题偏好失败（按跟随系统继续）: %v", err)
		case ok:
			m.mode = ParseMode(v) // 非法值由 ParseMode 兜底
		}
	}

	m.applier.Apply(m.mode)
	return m, nil
}

// Mode 返回当前外观模式。
func (m *Manager) Mode() Mode {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mode
}

// SetMode 切换外观模式：先立即生效，再持久化。
//
// 错误语义：持久化失败不影响本次切换（外观已生效），
// 仅导致"重启后不记得该偏好"，错误上抛由调用方提示。
// 传入非法值按 ModeSystem 处理（防御性）。
func (m *Manager) SetMode(mode Mode) error {
	mode = ParseMode(string(mode))

	m.mu.Lock()
	changed := mode != m.mode
	m.mode = mode
	m.mu.Unlock()

	// 无论是否变化都 Apply：幂等，且兜住"启动后被外部改回"的场景。
	m.applier.Apply(mode)

	var saveErr error
	if changed && m.store != nil {
		if err := m.store.SetSetting(SettingsKey, string(mode)); err != nil {
			saveErr = fmt.Errorf("theme: 保存主题偏好失败: %w", err)
		}
	}

	// 变更通知在锁外触发（监听器可能反调 Manager 方法）。
	if changed {
		m.notify(mode)
	}
	return saveErr
}

// OnChange 注册模式变更监听（同步在调用方线程触发），返回取消函数。
// 取消函数可安全重复调用（幂等）。
func (m *Manager) OnChange(fn func(Mode)) (off func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextSub++
	id := m.nextSub
	m.onChange[id] = fn
	return func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		delete(m.onChange, id) // 重复调用时键已不存在，无副作用
	}
}

// notify 广播模式变更（锁外调用，避免监听器反调 Manager 造成死锁）。
func (m *Manager) notify(mode Mode) {
	m.mu.Lock()
	fns := make([]func(Mode), 0, len(m.onChange))
	for _, fn := range m.onChange {
		fns = append(fns, fn)
	}
	m.mu.Unlock()
	for _, fn := range fns {
		fn(mode)
	}
}
