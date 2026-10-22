package hotkey

import (
	"fmt"
	"strings"
	"sync"
)

// Backend 是对 mygo.GlobalShortcut 的最小依赖抽象。
//
// mygo.GlobalShortcut（*GlobalShortcutModule）的方法集天然满足本接口，
// 生产代码直接传入即可；测试注入 fake，无需启动 mygo 应用环境。
type Backend interface {
	// Register 注册全局快捷键；失败（语法错/被占用/重复注册）返回 error。
	Register(acc string, fn func()) error
	// Unregister 注销快捷键；未注册时应为安全的 no-op。
	Unregister(acc string)
	// IsRegistered 报告该快捷键当前是否由本进程注册。
	IsRegistered(acc string) bool
}

// Manager 管理一组"命名动作"到全局快捷键的绑定，负责：
//   - 注册/注销/改键（改键先注册新键、成功后再注销旧键，失败保持原绑定）；
//   - 冲突检测：同一快捷键不允许绑定到两个动作，冲突返回 ErrConflict；
//   - 过期触发过滤：改键后旧键的残余触发不会命中 handler。
//
// 并发约定（重要）：
//   - 所有方法可并发调用；内部按"先登记后提交"的三段式流程执行，
//     任何阶段都不在调用 Backend 的同时持有内部锁；
//   - handler 由 Backend（mygo）在主线程调用。为避免与注册流程互相等待，
//     handler 内不得同步调用 Manager 的注册/注销方法，应只做 UI 展示类工作。
type Manager struct {
	backend Backend

	mu       sync.Mutex        // 保护下面三张表；绝不跨 Backend 调用持有
	byAction map[string]string // action → 规范快捷键
	byAcc    map[string]string // 规范快捷键 → action（冲突检测）
	handlers map[string]func() // action → 触发回调
}

// New 创建 Manager。backend 为 nil 视为编程错误。
func New(backend Backend) (*Manager, error) {
	if backend == nil {
		return nil, fmt.Errorf("hotkey: Backend 不能为空")
	}
	return &Manager{
		backend:  backend,
		byAction: map[string]string{},
		byAcc:    map[string]string{},
		handlers: map[string]func(){},
	}, nil
}

// Register 把 action 绑定到全局快捷键 acc。
//
// 语义：
//   - action 已绑定且 acc 相同：仅替换 handler，不动注册表（幂等）；
//   - action 已绑定但 acc 不同：等价于改键（注册新键成功后注销旧键）；
//   - action 未绑定：直接注册；若 acc 已被其他 action 占用返回 ErrConflict；
//   - Backend 注册失败（通常被系统/其他程序占用）返回 ErrConflict，且不影响已有绑定。
//
// handler 为 nil 或 action 为空视为编程错误。
func (m *Manager) Register(action, acc string, fn func()) error {
	if strings.TrimSpace(action) == "" {
		return fmt.Errorf("hotkey: action 不能为空")
	}
	if fn == nil {
		return fmt.Errorf("hotkey: action %q 的 handler 不能为空", action)
	}
	canon, err := normalize(acc) // 语法 + 安全策略 + 规范化
	if err != nil {
		return err
	}

	// ── 第一段：在锁内做决策登记（不调用 Backend）──
	m.mu.Lock()
	old, hadOld := m.byAction[action]
	if hadOld && old == canon {
		// 同键重复注册：只更新回调，避免无谓的注销/注册抖动。
		m.handlers[action] = fn
		m.mu.Unlock()
		return nil
	}
	if other, taken := m.byAcc[canon]; taken && other != action {
		m.mu.Unlock()
		return fmt.Errorf("%w: %q 已绑定到 %q", ErrConflict, canon, other)
	}
	// 预登记：占用目标键，防止并发 Register 同键互相通过检查。
	//（若最终注册失败或被他人抢先，由后续分支回滚。）
	m.byAcc[canon] = action
	m.byAction[action] = canon
	m.handlers[action] = fn
	m.mu.Unlock()

	// ── 第二段：调用 Backend（不持锁，避免主线程回调死锁）──
	if err := m.backend.Register(acc, m.wrap(action, canon)); err != nil {
		// 回滚预登记，保持表与 Backend 一致。
		m.mu.Lock()
		if m.byAcc[canon] == action {
			delete(m.byAcc, canon)
		}
		if m.byAction[action] == canon {
			if hadOld {
				m.byAction[action] = old // 恢复旧键映射
			} else {
				delete(m.byAction, action)
				delete(m.handlers, action)
			}
		}
		m.mu.Unlock()
		return fmt.Errorf("%w: 注册 %q 失败（可能已被其他程序占用）: %v", ErrConflict, canon, err)
	}

	// ── 第三段：注册成功，注销旧键（若有）──
	if hadOld {
		m.mu.Lock()
		stale := m.byAcc[old] == action && old != canon
		if stale {
			delete(m.byAcc, old) // 旧键不再归本 action
		}
		m.mu.Unlock()
		if stale {
			m.backend.Unregister(old) // 锁外注销，过期触发由 wrap() 过滤
		}
	}
	return nil
}

// Reregister 为已注册的 action 更换快捷键（handler 保持不变）。
// action 未注册返回 ErrUnknownAction。
func (m *Manager) Reregister(action, acc string) error {
	m.mu.Lock()
	fn := m.handlers[action]
	m.mu.Unlock()
	if fn == nil {
		return fmt.Errorf("%w: %q", ErrUnknownAction, action)
	}
	return m.Register(action, acc, fn)
}

// Unregister 注销 action 的全局快捷键。未注册时为安全的 no-op（幂等）。
func (m *Manager) Unregister(action string) {
	m.mu.Lock()
	canon, ok := m.byAction[action]
	delete(m.byAction, action)
	delete(m.handlers, action)
	if ok && m.byAcc[canon] == action {
		delete(m.byAcc, canon)
	}
	m.mu.Unlock()

	if ok {
		// 锁外注销：即使注销瞬间有残余触发，wrap() 也会因映射消失而忽略。
		m.backend.Unregister(canon)
	}
}

// UnregisterAll 注销全部绑定（应用退出时调用）。
func (m *Manager) UnregisterAll() {
	m.mu.Lock()
	accs := make([]string, 0, len(m.byAction))
	for action, canon := range m.byAction {
		accs = append(accs, canon)
		delete(m.byAction, action)
		delete(m.handlers, action)
		delete(m.byAcc, canon)
	}
	m.mu.Unlock()

	for _, canon := range accs {
		m.backend.Unregister(canon) // 锁外逐个注销
	}
}

// Accelerator 返回 action 当前绑定的规范快捷键。
func (m *Manager) Accelerator(action string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	canon, ok := m.byAction[action]
	return canon, ok
}

// Registered 报告 action 的快捷键当前是否真实注册在 Backend 上
// （可用于诊断"注册表有记录但被系统强制注销"的异常）。
func (m *Manager) Registered(action string) bool {
	m.mu.Lock()
	canon, ok := m.byAction[action]
	m.mu.Unlock()
	return ok && m.backend.IsRegistered(canon)
}

// handler 取回 action 的回调（settings.go 改键时复用）。
func (m *Manager) handler(action string) func() {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.handlers[action]
}

// wrap 生成带"过期触发过滤"的回调：
// 回调触发时校验 action 仍绑定到注册时的快捷键，避免改键瞬间
// 旧键的残余触发误执行新 handler。
func (m *Manager) wrap(action, canon string) func() {
	return func() {
		m.mu.Lock()
		fn := m.handlers[action]
		current := m.byAction[action]
		m.mu.Unlock()
		if fn != nil && current == canon {
			fn() // 锁外执行用户回调（主线程，可安全操作 UI）
		}
	}
}
