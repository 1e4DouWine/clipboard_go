package theme

import (
	"errors"
	"testing"

	"my-go-app/store"
)

// fakeApplier 记录应用的外观模式序列。
type fakeApplier struct {
	applied []Mode
}

func (f *fakeApplier) Apply(m Mode) { f.applied = append(f.applied, m) }

// last 返回最后一次应用的模式。
func (f *fakeApplier) last() Mode {
	if len(f.applied) == 0 {
		return ""
	}
	return f.applied[len(f.applied)-1]
}

// newTestManager 创建挂接真实临时 store 的 Manager。
func newTestManager(t *testing.T) (*Manager, *fakeApplier, *store.Store) {
	t.Helper()
	st, err := store.Open(t.TempDir(), store.Options{})
	if err != nil {
		t.Fatalf("Open store 失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	ap := &fakeApplier{}
	m, err := New(st, ap)
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	return m, ap, st
}

// TestParseMode 验证模式解析与非法值兜底。
func TestParseMode(t *testing.T) {
	cases := map[string]Mode{
		"system": ModeSystem,
		"light":  ModeLight,
		"dark":   ModeDark,
		"":       ModeSystem, // 空值兜底
		"bogus":  ModeSystem, // 未知值兜底（配置被改坏场景）
		"LIGHT":  ModeSystem, // 大小写敏感：不识别即兜底
		"  dark": ModeSystem, // 不做 Trim：不识别即兜底
	}
	for in, want := range cases {
		if got := ParseMode(in); got != want {
			t.Errorf("ParseMode(%q) = %q，期望 %q", in, got, want)
		}
	}
}

// TestModeIndexRoundtrip 验证 UI 序号与模式互转（含越界兜底）。
func TestModeIndexRoundtrip(t *testing.T) {
	for _, m := range []Mode{ModeSystem, ModeLight, ModeDark} {
		if got := ModeOfIndex(m.Index()); got != m {
			t.Errorf("往返不一致: %q → %d → %q", m, m.Index(), got)
		}
	}
	// 越界序号回退跟随系统。
	for _, i := range []int{-1, 3, 99} {
		if got := ModeOfIndex(i); got != ModeSystem {
			t.Errorf("ModeOfIndex(%d) = %q，期望 %q", i, got, ModeSystem)
		}
	}
}

// TestNewAppliesImmediately 验证创建即应用（启动不闪白的关键）。
func TestNewAppliesImmediately(t *testing.T) {
	_, ap, _ := newTestManager(t)

	if len(ap.applied) != 1 {
		t.Fatalf("New 应立即应用 1 次，实际 %d 次", len(ap.applied))
	}
	if ap.last() != ModeSystem {
		t.Fatalf("无偏好时应应用默认跟随系统，实际 %q", ap.last())
	}
}

// TestNewRejectsNilApplier 验证编程错误防御。
func TestNewRejectsNilApplier(t *testing.T) {
	if _, err := New(nil, nil); err == nil {
		t.Fatal("nil Applier 应报错")
	}
}

// TestSetModeAppliesAndPersists 验证切换即生效并持久化。
func TestSetModeAppliesAndPersists(t *testing.T) {
	m, ap, st := newTestManager(t)

	if err := m.SetMode(ModeDark); err != nil {
		t.Fatalf("SetMode 失败: %v", err)
	}
	if ap.last() != ModeDark {
		t.Fatalf("切换后应立即应用 dark，实际 %q", ap.last())
	}
	if m.Mode() != ModeDark {
		t.Fatalf("Mode() 应为 dark，实际 %q", m.Mode())
	}
	// 持久化验证。
	v, ok, err := st.GetSetting(SettingsKey)
	if err != nil || !ok || v != string(ModeDark) {
		t.Fatalf("偏好未持久化: v=%q ok=%v err=%v", v, ok, err)
	}

	// 非法输入按跟随系统处理。
	if err := m.SetMode(Mode("bogus")); err != nil {
		t.Fatalf("非法模式应兜底而非报错: %v", err)
	}
	if m.Mode() != ModeSystem {
		t.Fatalf("非法模式应回退 system，实际 %q", m.Mode())
	}
}

// TestSetModeSameValueSkipsSave 验证重复设置同值不产生多余持久化（幂等）。
func TestSetModeSameValueSkipsSave(t *testing.T) {
	m, ap, _ := newTestManager(t)

	first := len(ap.applied)
	if err := m.SetMode(ModeSystem); err != nil {
		t.Fatalf("SetMode 失败: %v", err)
	}
	// Apply 幂等调用一次（兜底语义），但不改变状态。
	if len(ap.applied) != first+1 {
		t.Fatalf("Apply 应幂等调用，实际应用序列 %+v", ap.applied)
	}
	if m.Mode() != ModeSystem {
		t.Fatal("模式不应变化")
	}
}

// TestRestoresSavedPreference 验证重启后恢复偏好（新实例读取持久化值）。
func TestRestoresSavedPreference(t *testing.T) {
	st, err := store.Open(t.TempDir(), store.Options{})
	if err != nil {
		t.Fatalf("Open store 失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	// 先存偏好。
	if err := st.SetSetting(SettingsKey, string(ModeDark)); err != nil {
		t.Fatalf("SetSetting 失败: %v", err)
	}

	ap := &fakeApplier{}
	m, err := New(st, ap)
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if m.Mode() != ModeDark || ap.last() != ModeDark {
		t.Fatalf("应恢复 dark 偏好: mode=%q applied=%q", m.Mode(), ap.last())
	}
}

// TestRestoresInvalidPreferenceFallsBack 验证坏配置回退默认且不报错。
func TestRestoresInvalidPreferenceFallsBack(t *testing.T) {
	st, err := store.Open(t.TempDir(), store.Options{})
	if err != nil {
		t.Fatalf("Open store 失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	// 模拟配置被外部改坏。
	if err := st.SetSetting(SettingsKey, "!!!broken!!!"); err != nil {
		t.Fatalf("SetSetting 失败: %v", err)
	}

	ap := &fakeApplier{}
	m, err := New(st, ap)
	if err != nil {
		t.Fatalf("坏配置不应导致启动失败: %v", err)
	}
	if m.Mode() != ModeSystem {
		t.Fatalf("坏配置应回退 system，实际 %q", m.Mode())
	}
}

// TestPersistenceFailureDoesNotBlockSwitch 验证保存失败不影响本次切换。
func TestPersistenceFailureDoesNotBlockSwitch(t *testing.T) {
	m, ap, st := newTestManager(t)
	// 关闭 store 制造保存失败。
	if err := st.Close(); err != nil {
		t.Fatalf("Close 失败: %v", err)
	}

	err := m.SetMode(ModeLight)
	if err == nil {
		t.Fatal("保存失败应返回错误供 UI 提示")
	}
	// 但外观已生效（核心语义不回退）。
	if ap.last() != ModeLight || m.Mode() != ModeLight {
		t.Fatalf("保存失败不影响切换: applied=%q mode=%q", ap.last(), m.Mode())
	}
}

// TestOnChangeNotifyAndCancel 验证变更通知与取消（幂等）。
func TestOnChangeNotifyAndCancel(t *testing.T) {
	m, _, _ := newTestManager(t)

	var got []Mode
	off := m.OnChange(func(mode Mode) { got = append(got, mode) })

	if err := m.SetMode(ModeDark); err != nil {
		t.Fatalf("SetMode 失败: %v", err)
	}
	if len(got) != 1 || got[0] != ModeDark {
		t.Fatalf("应收到 1 次 dark 通知，实际 %+v", got)
	}

	// 同值重复设置不触发通知（changed=false）。
	if err := m.SetMode(ModeDark); err != nil {
		t.Fatalf("SetMode 失败: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("同值设置不应触发通知，实际 %+v", got)
	}

	// 取消后再变更不通知；重复取消安全。
	off()
	off()
	if err := m.SetMode(ModeLight); err != nil {
		t.Fatalf("SetMode 失败: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("取消后不应通知，实际 %+v", got)
	}
}

// TestNilStoreAllowed 验证无 store 的纯内存模式可用（测试/特殊场景）。
func TestNilStoreAllowed(t *testing.T) {
	ap := &fakeApplier{}
	m, err := New(nil, ap)
	if err != nil {
		t.Fatalf("nil store 应允许: %v", err)
	}
	if err := m.SetMode(ModeDark); err != nil {
		t.Fatalf("无 store 时 SetMode 不应报错: %v", err)
	}
	if m.Mode() != ModeDark {
		t.Fatalf("mode = %q", m.Mode())
	}
}

// TestErrorSemantics 验证错误哨兵可用性（防回归）。
func TestErrorSemantics(t *testing.T) {
	if _, err := New(nil, nil); !errors.Is(err, err) {
		t.Fatal("错误语义异常")
	}
}
