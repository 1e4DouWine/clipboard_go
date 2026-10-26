package hotkey

import (
	"errors"
	"sync"
	"testing"

	"my-go-app/store"
)

// fakeBackend 是 Backend 的测试替身：
//   - failAcc 中的快捷键注册失败（模拟被其他程序占用）；
//   - handlers 记录已注册快捷键 → 回调，press 模拟按键触发。
type fakeBackend struct {
	mu       sync.Mutex
	handlers map[string]func() // 规范快捷键 → 回调
	failAcc  map[string]bool   // 这些快捷键注册失败
	regCalls int               // Register 调用次数（验证幂等/无谓抖动）
}

// blockingBackend pauses the first backend registration so the test can
// attempt a concurrent unregister at the exact transition point.
type blockingBackend struct {
	*fakeBackend
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *blockingBackend) Register(acc string, fn func()) error {
	b.once.Do(func() { close(b.entered) })
	<-b.release
	return b.fakeBackend.Register(acc, fn)
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{
		handlers: map[string]func(){},
		failAcc:  map[string]bool{},
	}
}

func (f *fakeBackend) Register(acc string, fn func()) error {
	canon, err := Canonical(acc)
	if err != nil {
		return err // 与 mygo 一致：语法错返回 error
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.regCalls++
	if f.failAcc[canon] {
		return errors.New("fake: hotkey taken by another application")
	}
	if _, exists := f.handlers[canon]; exists {
		return errors.New("fake: shortcut already registered")
	}
	f.handlers[canon] = fn
	return nil
}

func (f *fakeBackend) Unregister(acc string) {
	canon, err := Canonical(acc)
	if err != nil {
		return // 与 mygo 一致：非法值静默忽略
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.handlers, canon)
}

func (f *fakeBackend) IsRegistered(acc string) bool {
	canon, err := Canonical(acc)
	if err != nil {
		return false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.handlers[canon]
	return ok
}

// press 模拟按键触发；返回是否命中了注册回调。
func (f *fakeBackend) press(acc string) bool {
	canon, err := Canonical(acc)
	if err != nil {
		return false
	}
	f.mu.Lock()
	fn := f.handlers[canon]
	f.mu.Unlock()
	if fn == nil {
		return false
	}
	fn()
	return true
}

// wrapOf 取回某快捷键当前注册的 wrap 回调（用于模拟改键后的残余触发）。
func (f *fakeBackend) wrapOf(acc string) func() {
	canon, err := Canonical(acc)
	if err != nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.handlers[canon]
}

// registeredCount 返回当前注册的快捷键数量。
func (f *fakeBackend) registeredCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.handlers)
}

// newTestEnv 创建 Manager + 真实临时 store，测试结束自动清理。
func newTestEnv(t *testing.T) (*Manager, *fakeBackend, *store.Store) {
	t.Helper()
	st, err := store.Open(t.TempDir(), store.Options{})
	if err != nil {
		t.Fatalf("Open store 失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	backend := newFakeBackend()
	m := New(backend)
	return m, backend, st
}

// noop 是测试用的占位回调。
func noop() {}

// TestValidateSyntax 验证语法校验与 mygo accelerator 规则对齐。
func TestValidateSyntax(t *testing.T) {
	valid := []string{
		"Ctrl+Shift+V", "cmdorctrl+shift+space", "Alt+F4", "F5",
		"Ctrl+A", "Ctrl+1", "Ctrl++", "Ctrl+Num5", "Super+Up",
		"Ctrl + Shift + V", // 允许空格
		"Ctrl+Shift+Z", "CmdOrCtrl+Enter", "Win+Shift+S",
	}
	for _, acc := range valid {
		if err := Validate(acc); err != nil {
			t.Errorf("Validate(%q) 应通过，实际: %v", acc, err)
		}
	}

	invalid := []string{
		"", "   ", "Ctrl+", "Ctrl+Shift", // 缺按键/修饰键在末位
		"Ctrl+Foo", "Ctrl+f25", "Ctrl+f01", "Ctrl+Num12", // 未知/非规范键名
		"Ctrl++Shift", "Ctrl+Shift+", // 空段
		"Ctr+V", // 拼写错误的修饰键
	}
	for _, acc := range invalid {
		if err := Validate(acc); !errors.Is(err, ErrInvalidAccelerator) {
			t.Errorf("Validate(%q) 应返回 ErrInvalidAccelerator，实际: %v", acc, err)
		}
	}
}

// TestSafetyPolicy 验证全局安全策略：劫持输入的按键必须带 Ctrl/Alt/Super。
func TestSafetyPolicy(t *testing.T) {
	unsafe := []string{"a", "A", "Shift+A", "1", "Shift+Enter", "Space", "+"}
	for _, acc := range unsafe {
		if err := Validate(acc); !errors.Is(err, ErrUnsafeAccelerator) {
			t.Errorf("Validate(%q) 应返回 ErrUnsafeAccelerator，实际: %v", acc, err)
		}
	}

	safe := []string{"Ctrl+A", "Alt+A", "Super+A", "F5", "Ctrl+Shift+V", "PrintScreen"}
	for _, acc := range safe {
		if err := Validate(acc); err != nil {
			t.Errorf("Validate(%q) 应通过，实际: %v", acc, err)
		}
	}
}

// TestCanonical 验证规范化（大小写、修饰键顺序、别名）。
func TestCanonical(t *testing.T) {
	cases := map[string]string{
		"ctrl+shift+v":    "Ctrl+Shift+V",
		"Shift+Ctrl+V":    "Ctrl+Shift+V", // 修饰键顺序归一
		"cmdorctrl+space": "Ctrl+Space",   // 非 darwin 平台解析为 Ctrl
		"control+alt+del": "Ctrl+Alt+Delete",
		"ctrl+a":          "Ctrl+A",
		"ctrl+f12":        "Ctrl+F12",
		"ctrl+num5":       "Ctrl+Num5",
	}
	for in, want := range cases {
		got, err := Canonical(in)
		if err != nil {
			t.Fatalf("Canonical(%q) 失败: %v", in, err)
		}
		if got != want {
			t.Errorf("Canonical(%q) = %q，期望 %q", in, got, want)
		}
	}
}

// TestRegisterAndPress 验证基本注册与触发。
func TestRegisterAndPress(t *testing.T) {
	m, backend, _ := newTestEnv(t)

	fired := false
	if err := m.Register(ActionToggleWindow, "Ctrl+Shift+V", func() { fired = true }); err != nil {
		t.Fatalf("Register 失败: %v", err)
	}
	if !backend.press("Ctrl+Shift+V") || !fired {
		t.Fatal("按键触发未执行回调")
	}
	if got, ok := m.Accelerator(ActionToggleWindow); !ok || got != "Ctrl+Shift+V" {
		t.Fatalf("Accelerator = (%q, %v)，期望 (Ctrl+Shift+V, true)", got, ok)
	}
	if !m.Registered(ActionToggleWindow) {
		t.Fatal("Registered 应为 true")
	}
}

// TestRegisterSameAccSameAction 验证同键重复注册只换回调、不抖动注册表。
func TestRegisterSameAccSameAction(t *testing.T) {
	m, backend, _ := newTestEnv(t)

	if err := m.Register("act", "Ctrl+Shift+A", func() {}); err != nil {
		t.Fatalf("Register 失败: %v", err)
	}
	calls := backend.regCalls
	if err := m.Register("act", "Ctrl+Shift+A", noop); err != nil {
		t.Fatalf("重复注册失败: %v", err)
	}
	if backend.regCalls != calls {
		t.Fatal("同键重复注册不应调用 Backend.Register")
	}

	// 新回调生效。
	fired := false
	if err := m.Register("act", "Ctrl+Shift+A", func() { fired = true }); err != nil {
		t.Fatalf("替换回调失败: %v", err)
	}
	backend.press("Ctrl+Shift+A")
	if !fired {
		t.Fatal("回调未更新")
	}
}

// TestRegisterConflictBetweenActions 验证同一快捷键不能绑定两个动作。
func TestRegisterConflictBetweenActions(t *testing.T) {
	m, _, _ := newTestEnv(t)

	if err := m.Register("act-a", "Ctrl+Shift+B", noop); err != nil {
		t.Fatalf("Register 失败: %v", err)
	}
	err := m.Register("act-b", "ctrl+shift+b", noop) // 即使写法不同，规范化后仍冲突
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("期望 ErrConflict，实际: %v", err)
	}
	// 原绑定不受影响。
	if got, ok := m.Accelerator("act-a"); !ok || got != "Ctrl+Shift+B" {
		t.Fatalf("原绑定被破坏: (%q, %v)", got, ok)
	}
}

// TestRegisterBackendConflict 验证被系统/其他程序占用时的错误与状态回滚。
func TestRegisterBackendConflict(t *testing.T) {
	m, backend, _ := newTestEnv(t)
	backend.failAcc["Ctrl+Shift+C"] = true // 模拟被其他程序占用

	err := m.Register("act", "Ctrl+Shift+C", noop)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("期望 ErrConflict，实际: %v", err)
	}
	if _, ok := m.Accelerator("act"); ok {
		t.Fatal("注册失败不应留下绑定记录")
	}
	// 失败后换一个键应能正常注册（冲突不阻塞后续动作）。
	if err := m.Register("act", "Ctrl+Shift+D", noop); err != nil {
		t.Fatalf("换键注册失败: %v", err)
	}
}

// TestReregisterChangeKey 验证改键：新键生效、旧键注销、回调保持不变。
func TestReregisterChangeKey(t *testing.T) {
	m, backend, _ := newTestEnv(t)

	fired := false
	if err := m.Register("act", "Ctrl+Shift+E", func() { fired = true }); err != nil {
		t.Fatalf("Register 失败: %v", err)
	}
	if err := m.Reregister("act", "Ctrl+Shift+F"); err != nil {
		t.Fatalf("Reregister 失败: %v", err)
	}

	if backend.IsRegistered("Ctrl+Shift+E") {
		t.Fatal("旧键应已注销")
	}
	if backend.press("Ctrl+Shift+E") || fired {
		t.Fatal("旧键触发应被忽略")
	}
	// Reregister 不换回调：新键触发执行的仍是原 handler。
	if !backend.press("Ctrl+Shift+F") {
		t.Fatal("新键应已注册")
	}
	if !fired {
		t.Fatal("新键应触发原回调")
	}

	// Reregister 未知动作应报 ErrUnknownAction。
	if err := m.Reregister("ghost", "Ctrl+Shift+G"); !errors.Is(err, ErrUnknownAction) {
		t.Fatalf("期望 ErrUnknownAction，实际: %v", err)
	}
}

// TestReregisterFailureKeepsOld 验证改键失败时旧绑定保持不变。
func TestReregisterFailureKeepsOld(t *testing.T) {
	m, backend, _ := newTestEnv(t)
	backend.failAcc["Ctrl+Shift+H"] = true

	if err := m.Register("act", "Ctrl+Shift+I", noop); err != nil {
		t.Fatalf("Register 失败: %v", err)
	}
	err := m.Reregister("act", "Ctrl+Shift+H")
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("期望 ErrConflict，实际: %v", err)
	}
	// 旧键仍在、绑定不变。
	if got, ok := m.Accelerator("act"); !ok || got != "Ctrl+Shift+I" {
		t.Fatalf("改键失败应保持原绑定，实际 (%q, %v)", got, ok)
	}
	if !backend.IsRegistered("Ctrl+Shift+I") {
		t.Fatal("旧键应仍注册在 Backend")
	}
}

// TestUnregisterIdempotent 验证注销幂等与清理。
func TestUnregisterIdempotent(t *testing.T) {
	m, backend, _ := newTestEnv(t)

	if err := m.Register("act", "Ctrl+Shift+J", noop); err != nil {
		t.Fatalf("Register 失败: %v", err)
	}
	m.Unregister("act")
	m.Unregister("act") // 重复注销不应 panic
	m.Unregister("ghost")

	if backend.IsRegistered("Ctrl+Shift+J") {
		t.Fatal("注销后键仍注册")
	}
	if _, ok := m.Accelerator("act"); ok {
		t.Fatal("注销后绑定记录应清除")
	}
	// 注销后同键可被其他动作注册。
	if err := m.Register("act2", "Ctrl+Shift+J", noop); err != nil {
		t.Fatalf("复用已注销的键失败: %v", err)
	}
}

// TestUnregisterAll 验证批量注销。
func TestUnregisterAll(t *testing.T) {
	m, backend, _ := newTestEnv(t)

	for i, acc := range []string{"Ctrl+Shift+K", "Ctrl+Shift+L", "Ctrl+Shift+M"} {
		if err := m.Register(string(rune('a'+i)), acc, noop); err != nil {
			t.Fatalf("Register(%q) 失败: %v", acc, err)
		}
	}
	m.UnregisterAll()
	if n := backend.registeredCount(); n != 0 {
		t.Fatalf("UnregisterAll 后应清空，实际残留 %d 个", n)
	}
	m.UnregisterAll() // 幂等
}

// TestDefaultAcceleratorsValid 验证内置默认快捷键全部通过语法+安全校验。
func TestDefaultAcceleratorsValid(t *testing.T) {
	for action, acc := range DefaultAccelerators {
		if err := Validate(acc); err != nil {
			t.Errorf("默认快捷键 %q = %q 未通过校验: %v", action, acc, err)
		}
	}
	if DefaultAccelerators[ActionToggleWindow] == "" {
		t.Fatal("核心动作 toggle-window 必须有默认快捷键")
	}
}

// TestSettingsRoundTrip 验证自定义快捷键持久化往返与清空。
func TestSettingsRoundTrip(t *testing.T) {
	_, _, st := newTestEnv(t)

	if _, ok, err := LoadAccelerator(st, ActionToggleWindow); err != nil || ok {
		t.Fatalf("未设置时应无自定义值: ok=%v err=%v", ok, err)
	}

	// 存入非规范写法，读回时应为规范形式。
	if err := SaveAccelerator(st, ActionToggleWindow, "ctrl+alt+v"); err != nil {
		t.Fatalf("SaveAccelerator 失败: %v", err)
	}
	v, ok, err := LoadAccelerator(st, ActionToggleWindow)
	if err != nil || !ok || v != "Ctrl+Alt+V" {
		t.Fatalf("读回不一致: v=%q ok=%v err=%v", v, ok, err)
	}

	// 非法值拒绝写入。
	if err := SaveAccelerator(st, ActionToggleWindow, "Ctrl+Nope"); !errors.Is(err, ErrInvalidAccelerator) {
		t.Fatalf("非法值应拒绝写入，实际: %v", err)
	}

	// 空串清空自定义。
	if err := SaveAccelerator(st, ActionToggleWindow, ""); err != nil {
		t.Fatalf("清空失败: %v", err)
	}
	if _, ok, _ := LoadAccelerator(st, ActionToggleWindow); ok {
		t.Fatal("清空后应无自定义值")
	}
}

// TestEffectiveAccelerator 验证自定义优先、默认兜底、坏配置降级。
func TestEffectiveAccelerator(t *testing.T) {
	_, _, st := newTestEnv(t)

	// 无自定义 → 默认值。
	acc, err := EffectiveAccelerator(st, ActionToggleWindow)
	if err != nil || acc != DefaultAccelerators[ActionToggleWindow] {
		t.Fatalf("应返回默认键: acc=%q err=%v", acc, err)
	}

	// 有自定义 → 自定义值。
	if err := SaveAccelerator(st, ActionToggleWindow, "Ctrl+Alt+T"); err != nil {
		t.Fatalf("SaveAccelerator 失败: %v", err)
	}
	if acc, err := EffectiveAccelerator(st, ActionToggleWindow); err != nil || acc != "Ctrl+Alt+T" {
		t.Fatalf("应返回自定义键: acc=%q err=%v", acc, err)
	}

	// 配置被外部改坏 → 回退默认键 + 提示型错误。
	if err := st.SetSetting(SettingKey(ActionToggleWindow), "garbage-key!!"); err != nil {
		t.Fatalf("SetSetting 失败: %v", err)
	}
	acc, err = EffectiveAccelerator(st, ActionToggleWindow)
	if !errors.Is(err, ErrInvalidAccelerator) {
		t.Fatalf("坏配置应返回 ErrInvalidAccelerator 提示，实际: %v", err)
	}
	if acc != DefaultAccelerators[ActionToggleWindow] {
		t.Fatalf("坏配置应回退默认键，实际 %q", acc)
	}

	// 未知动作。
	if _, err := EffectiveAccelerator(st, "ghost"); !errors.Is(err, ErrUnknownAction) {
		t.Fatalf("未知动作应返回 ErrUnknownAction，实际: %v", err)
	}
}

// TestRegisterWithSettings 验证"自定义优先、默认兜底"注册。
func TestRegisterWithSettings(t *testing.T) {
	m, backend, st := newTestEnv(t)

	// 默认键注册。
	acc, err := RegisterWithSettings(m, st, ActionToggleWindow, noop)
	if err != nil || acc != "Ctrl+Shift+V" {
		t.Fatalf("应以默认键注册: acc=%q err=%v", acc, err)
	}

	// 自定义键注册（新 Manager 环境）。
	m2, backend2, _ := newTestEnv(t)
	if err := SaveAccelerator(st, ActionToggleWindow, "Ctrl+Alt+Y"); err != nil {
		t.Fatalf("SaveAccelerator 失败: %v", err)
	}
	acc, err = RegisterWithSettings(m2, st, ActionToggleWindow, noop)
	if err != nil || acc != "Ctrl+Alt+Y" {
		t.Fatalf("应以自定义键注册: acc=%q err=%v", acc, err)
	}
	if !backend2.IsRegistered("Ctrl+Alt+Y") {
		t.Fatal("自定义键未注册到 Backend")
	}
	_ = backend

	// 坏配置 → 用默认键注册，同时返回提示型错误。
	m3, backend3, _ := newTestEnv(t)
	if err := st.SetSetting(SettingKey(ActionToggleWindow), "broken!!"); err != nil {
		t.Fatalf("SetSetting 失败: %v", err)
	}
	acc, err = RegisterWithSettings(m3, st, ActionToggleWindow, noop)
	if !errors.Is(err, ErrInvalidAccelerator) {
		t.Fatalf("应返回提示型错误，实际: %v", err)
	}
	if acc != "Ctrl+Shift+V" || !backend3.IsRegistered("Ctrl+Shift+V") {
		t.Fatalf("坏配置应回退默认键注册，acc=%q", acc)
	}
}

// TestChangeTransactional 验证改键的事务语义。
func TestChangeTransactional(t *testing.T) {
	m, backend, st := newTestEnv(t)

	if err := m.Register(ActionToggleWindow, "Ctrl+Shift+V", noop); err != nil {
		t.Fatalf("Register 失败: %v", err)
	}

	// 成功改键：注册表与配置库同步更新。
	if err := Change(m, st, ActionToggleWindow, "ctrl+alt+z"); err != nil {
		t.Fatalf("Change 失败: %v", err)
	}
	if got, _ := m.Accelerator(ActionToggleWindow); got != "Ctrl+Alt+Z" {
		t.Fatalf("注册表未更新: %q", got)
	}
	if v, ok, _ := LoadAccelerator(st, ActionToggleWindow); !ok || v != "Ctrl+Alt+Z" {
		t.Fatalf("配置未更新: v=%q ok=%v", v, ok)
	}
	if backend.IsRegistered("Ctrl+Shift+V") {
		t.Fatal("旧键应已注销")
	}

	// 非法新键：无任何副作用。
	if err := Change(m, st, ActionToggleWindow, "bogus"); !errors.Is(err, ErrInvalidAccelerator) {
		t.Fatalf("非法键应报错，实际: %v", err)
	}
	if got, _ := m.Accelerator(ActionToggleWindow); got != "Ctrl+Alt+Z" {
		t.Fatalf("非法改键不应影响绑定: %q", got)
	}

	// 保存失败（store 已关闭）：注册回滚到旧键。
	if err := st.Close(); err != nil {
		t.Fatalf("Close 失败: %v", err)
	}
	err := Change(m, st, ActionToggleWindow, "Ctrl+Alt+W")
	if err == nil {
		t.Fatal("store 关闭后 Change 应报错")
	}
	if got, _ := m.Accelerator(ActionToggleWindow); got != "Ctrl+Alt+Z" {
		t.Fatalf("保存失败应回滚到旧键，实际 %q", got)
	}
	if !backend.IsRegistered("Ctrl+Alt+Z") {
		t.Fatal("保存失败后旧键应仍注册")
	}

	// 未知动作。
	if err := Change(m, st, "ghost", "Ctrl+Alt+Q"); !errors.Is(err, ErrUnknownAction) {
		t.Fatalf("未知动作应报 ErrUnknownAction，实际: %v", err)
	}
}

// TestExpiredTriggerFiltered 验证改键瞬间的旧键残余触发不误执行 handler。
func TestExpiredTriggerFiltered(t *testing.T) {
	m, backend, _ := newTestEnv(t)

	oldFired, newFired := false, false
	if err := m.Register("act", "Ctrl+Shift+N", func() { oldFired = true }); err != nil {
		t.Fatalf("Register 失败: %v", err)
	}
	// 取出旧键的 wrap 回调（模拟 Backend 尚未送达/注销瞬间的残余触发）。
	staleWrap := backend.wrapOf("Ctrl+Shift+N")

	// 改键到新回调。
	if err := m.Register("act", "Ctrl+Shift+O", func() { newFired = true }); err != nil {
		t.Fatalf("改键失败: %v", err)
	}

	// 旧键的残余触发：既不应执行旧回调，也不应误执行新回调。
	if staleWrap != nil {
		staleWrap()
	}
	if oldFired || newFired {
		t.Fatalf("过期触发被误执行: oldFired=%v newFired=%v", oldFired, newFired)
	}

	// 正常路径：新键触发新回调。
	if !backend.press("Ctrl+Shift+O") || !newFired {
		t.Fatal("新键应触发新回调")
	}
}

// TestConcurrentSafety 验证并发注册/注销/触发不产生数据竞争（配合 -race）。
func TestConcurrentSafety(t *testing.T) {
	m, backend, _ := newTestEnv(t)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			action := string(rune('a' + i))
			acc := "Ctrl+Shift+" + string(rune('A'+i))
			_ = m.Register(action, acc, func() {})
			_ = m.Reregister(action, "Ctrl+Alt+"+string(rune('A'+i)))
			backend.press("Ctrl+Alt+" + string(rune('A'+i)))
			m.Unregister(action)
		}(i)
	}
	wg.Wait()
	if n := backend.registeredCount(); n != 0 {
		t.Fatalf("并发注销后应清空，实际残留 %d 个", n)
	}
}

func TestRegisterAndUnregisterAreAtomicWithBackend(t *testing.T) {
	backend := &blockingBackend{
		fakeBackend: newFakeBackend(),
		entered:     make(chan struct{}),
		release:     make(chan struct{}),
	}
	m := New(backend)

	registered := make(chan error, 1)
	go func() { registered <- m.Register("act", "Ctrl+Shift+R", noop) }()
	<-backend.entered

	unregistered := make(chan struct{})
	go func() {
		m.Unregister("act")
		close(unregistered)
	}()
	select {
	case <-unregistered:
		t.Fatal("Unregister 不应在 Register 的后端调用完成前修改状态")
	default:
	}

	close(backend.release)
	if err := <-registered; err != nil {
		t.Fatalf("Register 失败: %v", err)
	}
	<-unregistered
	if backend.registeredCount() != 0 {
		t.Fatalf("并发注销后后端不应残留快捷键，实际 %d", backend.registeredCount())
	}
	if _, ok := m.Accelerator("act"); ok {
		t.Fatal("并发注销后 Manager 不应残留绑定")
	}
}
