package main

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"my-go-app/store"
)

// TestViewEmpty 验证空状态引导文案。
func TestViewEmpty(t *testing.T) {
	a := &app{}
	tt := ui.NewTester(a.view, 560, 520)
	if !tt.HasText("Clipboard History") {
		t.Errorf("expected title, texts %q", tt.Texts())
	}
	if !tt.HasText("Copy some text anywhere") {
		t.Errorf("expected empty-state hint, texts %q", tt.Texts())
	}
}

// TestViewEmptyFiltered 验证过滤无结果时的空状态文案分流。
func TestViewEmptyFiltered(t *testing.T) {
	a := &app{query: "no-such-thing"}
	tt := ui.NewTester(a.view, 560, 520)
	if !tt.HasText("No matches") {
		t.Errorf("expected filtered empty state, texts %q", tt.Texts())
	}
	if tt.HasText("Copy some text anywhere") {
		t.Errorf("filtered empty state should not show capture hint, texts %q", tt.Texts())
	}
}

// TestViewEmptyCategoryFiltered 验证仅分类过滤（无关键字）也走"No matches"。
func TestViewEmptyCategoryFiltered(t *testing.T) {
	a := &app{filter: filterImage}
	tt := ui.NewTester(a.view, 560, 520)
	if !tt.HasText("No matches") {
		t.Errorf("expected filtered empty state, texts %q", tt.Texts())
	}
}

// TestViewWithItems 验证条目预览与元信息渲染。
func TestViewWithItems(t *testing.T) {
	a := &app{items: []store.Entry{
		{ID: 1, Kind: store.KindText, Preview: "second entry", CopiedAt: 1700000000000},
		{ID: 2, Kind: store.KindLink, Preview: "https://example.com", CopiedAt: 1700000000000, SourceApp: "Chrome"},
	}}
	tt := ui.NewTester(a.view, 560, 520)
	if !tt.HasText("second entry") || !tt.HasText("https://example.com") {
		t.Errorf("expected both items, texts %q", tt.Texts())
	}
	if !tt.HasText("2 items") {
		t.Errorf("expected item count, texts %q", tt.Texts())
	}
	// 类型徽标。
	if !tt.HasText("URL") {
		t.Errorf("expected URL badge, texts %q", tt.Texts())
	}
}

// TestViewFavoriteStar 验证收藏星标展示。
func TestViewFavoriteStar(t *testing.T) {
	a := &app{items: []store.Entry{
		{ID: 1, Kind: store.KindText, Preview: "starred", IsFavorite: true, CopiedAt: 1700000000000},
	}}
	tt := ui.NewTester(a.view, 560, 520)
	if !tt.HasText("★") {
		t.Errorf("expected favorite star, texts %q", tt.Texts())
	}
}

// TestViewStatusMessage 验证状态栏提示优先于快捷键帮助。
func TestViewStatusMessage(t *testing.T) {
	msg := T("Copied to clipboard (auto-paste failed, paste manually)")
	a := &app{status: msg}
	tt := ui.NewTester(a.view, 560, 520)
	if !tt.HasText(msg) {
		t.Errorf("expected status message %q, texts %q", msg, tt.Texts())
	}
}

// TestSafeViewRecoversFromPanic 验证视图 panic 时降级渲染而非崩溃。
func TestSafeViewRecoversFromPanic(t *testing.T) {
	// 构造必 panic 的视图，guardView 应捕获并渲染降级界面。
	panicky := func(c *ui.Context) { panic("render boom") }
	tt := ui.NewTester(guardView(panicky), 560, 520)

	if !tt.HasText(T("UI render error, showing fallback")) {
		t.Errorf("应渲染降级界面, texts %q", tt.Texts())
	}

	// 正常视图经 guardView 包装后内容不受影响。
	a := &app{}
	tt2 := ui.NewTester(guardView(a.view), 560, 520)
	if !tt2.HasText("Clipboard History") {
		t.Errorf("正常视图应完整渲染, texts %q", tt2.Texts())
	}
}

// TestViewSettingsModal 验证设置 Modal：主界面无主题/自启控件，打开后可见。
func TestViewSettingsModal(t *testing.T) {
	lang = LangEN
	defer func() { lang = LangEN }()

	// 主界面：标题栏无 Appearance/主题分段，状态栏无 Launch at login。
	a := &app{themeIdx: 2, langIdx: 0}
	tt := ui.NewTester(a.view, 560, 520)
	if tt.HasText("Auto") || tt.HasText("Light") || tt.HasText("Dark") {
		t.Errorf("theme switcher should not be on main view, texts %q", tt.Texts())
	}
	if tt.HasText("Launch at login") {
		t.Errorf("autostart should not be on main view, texts %q", tt.Texts())
	}

	// 打开设置：主题标签 + 语言 + 开机自启一并出现（独立 app，避免 ListState 双绑）。
	b := &app{themeIdx: 2, langIdx: 0, settingsOpen: true}
	tt2 := ui.NewTester(b.view, 560, 520)
	if !tt2.HasText("Settings") || !tt2.HasText("Language") || !tt2.HasText("Theme") {
		t.Errorf("expected settings modal labels, texts %q", tt2.Texts())
	}
	if !tt2.HasText("Auto") || !tt2.HasText("Light") || !tt2.HasText("Dark") {
		t.Errorf("expected theme switcher in settings, texts %q", tt2.Texts())
	}
	if !tt2.HasText("Launch at login") {
		t.Errorf("expected autostart in settings, texts %q", tt2.Texts())
	}
	if !tt2.HasText("English") || !tt2.HasText("中文") {
		t.Errorf("expected language options, texts %q", tt2.Texts())
	}
}

// TestViewChineseLanguage 验证切换中文后整窗文案变化。
func TestViewChineseLanguage(t *testing.T) {
	lang = LangZH
	defer func() { lang = LangEN }()

	a := &app{}
	tt := ui.NewTester(a.view, 560, 520)
	if !tt.HasText("剪贴板历史") {
		t.Errorf("expected Chinese title, texts %q", tt.Texts())
	}
	if !tt.HasText("搜索历史") {
		t.Errorf("expected Chinese search label, texts %q", tt.Texts())
	}
	if !tt.HasText("在任意处复制文本") {
		t.Errorf("expected Chinese empty state, texts %q", tt.Texts())
	}
	if tt.HasText("Clipboard History") || tt.HasText("Search history") {
		t.Errorf("English chrome should not appear in ZH, texts %q", tt.Texts())
	}

	// 设置页中文标签
	b := &app{settingsOpen: true}
	tt2 := ui.NewTester(b.view, 560, 520)
	if !tt2.HasText("设置") || !tt2.HasText("语言") || !tt2.HasText("主题") {
		t.Errorf("expected Chinese settings labels, texts %q", tt2.Texts())
	}
	if !tt2.HasText("自动") || !tt2.HasText("浅色") || !tt2.HasText("暗黑") {
		t.Errorf("expected Chinese theme labels, texts %q", tt2.Texts())
	}
}

// TestFluentThemeRadius 验证 Fluent 度量覆盖：控件圆角收到 4（Win11）。
func TestFluentThemeRadius(t *testing.T) {
	var got float32
	tt := ui.NewTester(func(c *ui.Context) {
		th := applyFluentTheme(c)
		got = th.Radius
		ui.Text(c, "probe")
	}, 560, 520)
	_ = tt.HasText("probe")
	if got != 4 {
		t.Fatalf("Fluent Radius = %v, want 4", got)
	}
}

// TestFluentHeaderCompact 验证标题栏紧凑：标题在窗口上沿附近，计数与切换器同排。
func TestFluentHeaderCompact(t *testing.T) {
	a := &app{items: []store.Entry{
		{ID: 1, Kind: store.KindText, Preview: "compact-row", CopiedAt: 1700000000000},
	}}
	tt := ui.NewTester(a.view, 560, 520)
	title, ok := tt.Find("Clipboard History")
	if !ok {
		t.Fatalf("title not found, texts %q", tt.Texts())
	}
	if title.Y > 48 {
		t.Errorf("title Y = %v, want compact header (Y <= 48)", title.Y)
	}
	if _, ok := tt.Find("1 items"); !ok {
		t.Errorf("expected quiet item count, texts %q", tt.Texts())
	}
}

// TestFluentShortcutSeparator 验证状态栏快捷键以 · 分隔（安静页脚）。
func TestFluentShortcutSeparator(t *testing.T) {
	a := &app{}
	tt := ui.NewTester(a.view, 560, 520)
	if !tt.HasText("·") {
		t.Errorf("expected shortcut help with · separators, texts %q", tt.Texts())
	}
}

// TestViewKeyboardNav 验证键盘选择与激活编排（↑↓/Enter）。
func TestViewKeyboardNav(t *testing.T) {
	a := &app{items: []store.Entry{
		{ID: 1, Kind: store.KindText, Preview: "first", FullText: "first", CopiedAt: 1700000000000},
		{ID: 2, Kind: store.KindText, Preview: "second", FullText: "second", CopiedAt: 1700000000000},
	}}
	clip := &fakeClip{}
	win := &fakeWin{events: &[]string{}}
	paste := &fakePaster{events: &[]string{}}
	a.clip, a.win, a.paste = clip, win, paste

	tt := ui.NewTester(a.view, 560, 520)
	tt.Key(0, ui.KeyDown)  // 选中第二条
	tt.Key(0, ui.KeyEnter) // 粘贴选中项

	if a.chosen != 1 {
		t.Fatalf("chosen 应为 1，实际 %d", a.chosen)
	}
	if len(clip.texts) != 1 || clip.texts[0] != "second" {
		t.Fatalf("Enter 应粘贴选中项，实际 %+v", clip.texts)
	}
	if win.visible {
		t.Fatal("粘贴后窗口应隐藏")
	}
}
