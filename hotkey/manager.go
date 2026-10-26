package hotkey

import (
	"fmt"
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
//   - 所有方法可并发调用；注册/注销操作由 backendMu 串行化，
//     任何阶段都不在调用 Backend 的同时持有内部状态锁；
//   - handler 由 Backend（mygo）在主线程调用。为避免与注册流程互相等待，
//     handler 内不得同步调用 Manager 的注册/注销方法，应只做 UI 展示类工作。
type Manager struct {
	backend Backend

	// backendMu 串行化状态变更与后端调用，mu 允许后端回调独立读取绑定。
	backendMu sync.Mutex

	mu       sync.Mutex        // 保护下面三张表；绝不跨 Backend 调用持有
	byAction map[string]string // action → 规范快捷键
	byAcc    map[string]string // 规范快捷键 → action（冲突检测）
	handlers map[string]func() // action → 触发回调
}

// New 创建 Manager，backend 必须由应用初始化时提供。
func New(backend Backend) *Manager {
	return &Manager{
		backend:  backend,
		byAction: map[string]string{},
		byAcc:    map[string]string{},
		handlers: map[string]func(){},
	}
}

// Register 把 action 绑定到全局快捷键 acc。
//
// 语义：
//   - action 已绑定且 acc 相同：仅替换 handler，不动注册表（幂等）；
//   - action 已绑定但 acc 不同：等价于改键（注册新键成功后注销旧键）；
//   - action 未绑定：直接注册；若 acc 已被其他 action 占用返回 ErrConflict；
//   - Backend 注册失败（通常被系统/其他程序占用）返回 ErrConflict，且不影响已有绑定。
//
// action 和 fn 由应用定义；acc 按用户输入校验。
func (m *Manager) Register(action, acc string, fn func()) error {
	canon, err := normalize(acc) // 语法 + 安全策略 + 规范化
	if err != nil {
		return err
	}
	m.backendMu.Lock()
	defer m.backendMu.Unlock()
	return m.registerCanonical(action, canon, fn)
}

// registerCanonical performs one complete registration transition. The
// caller must hold backendMu so state and backend cannot be interleaved with
// another registration or unregistration.
func (m *Manager) registerCanonical(action, canon string, fn func()) error {
	m.mu.Lock()
	old, hadOld := m.byAction[action]
	if hadOld && old == canon {
		// 同键重复注册：只更新回调，避免无谓的注销/注册抖动。
		m.handlers[action] = fn
		m.mu.Unlock()
		return nil
	}
	if other, taken := m.byAcc[canon]; taken {
		m.mu.Unlock()
		return fmt.Errorf("%w: %q 已绑定到 %q", ErrConflict, canon, other)
	}
	m.mu.Unlock()

	if err := m.backend.Register(canon, m.wrap(action, canon)); err != nil {
		return fmt.Errorf("%w: 注册 %q 失败（可能已被其他程序占用）: %v", ErrConflict, canon, err)
	}

	// backendMu 串行化注册操作；后端成功后才提交新绑定。
	m.mu.Lock()
	m.byAcc[canon] = action
	m.byAction[action] = canon
	m.handlers[action] = fn
	delete(m.byAcc, old)
	m.mu.Unlock()
	if hadOld {
		m.backend.Unregister(old)
	}
	return nil
}

// Reregister 为已注册的 action 更换快捷键（handler 保持不变）。
// action 未注册返回 ErrUnknownAction。
func (m *Manager) Reregister(action, acc string) error {
	m.backendMu.Lock()
	defer m.backendMu.Unlock()
	fn := m.handler(action)
	if fn == nil {
		return fmt.Errorf("%w: %q", ErrUnknownAction, action)
	}
	canon, err := normalize(acc)
	if err != nil {
		return err
	}
	return m.registerCanonical(action, canon, fn)
}

// Unregister 注销 action 的全局快捷键。未注册时为安全的 no-op（幂等）。
func (m *Manager) Unregister(action string) {
	m.backendMu.Lock()
	defer m.backendMu.Unlock()
	m.unregisterCanonical(action)
}

func (m *Manager) unregisterCanonical(action string) {
	m.mu.Lock()
	canon, ok := m.byAction[action]
	delete(m.byAction, action)
	delete(m.handlers, action)
	if ok {
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
	m.backendMu.Lock()
	defer m.backendMu.Unlock()
	m.mu.Lock()
	accs := make([]string, 0, len(m.byAction))
	for _, canon := range m.byAction {
		accs = append(accs, canon)
	}
	clear(m.byAction)
	clear(m.handlers)
	clear(m.byAcc)
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
	m.backendMu.Lock()
	defer m.backendMu.Unlock()
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
