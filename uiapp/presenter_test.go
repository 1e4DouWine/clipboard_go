package uiapp

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"my-go-app/i18n"
	"my-go-app/store"
)

// TestViewEmpty 验证空状态引导文案。
func TestViewEmpty(t *testing.T) {
	h := &harness{}
	tt := ui.NewTester(h.presenter().View, 560, 520)
	if !tt.HasText("Clipboard History") {
		t.Errorf("expected title, texts %q", tt.Texts())
	}
	if !tt.HasText("Copy some text anywhere") {
		t.Errorf("expected empty-state hint, texts %q", tt.Texts())
	}
}

// TestViewEmptyFiltered 验证过滤无结果时的空状态文案分流。
func TestViewEmptyFiltered(t *testing.T) {
	h := &harness{query: "no-such-thing"}
	tt := ui.NewTester(h.presenter().View, 560, 520)
	if !tt.HasText("No matches") {
		t.Errorf("expected filtered empty state, texts %q", tt.Texts())
	}
	if tt.HasText("Copy some text anywhere") {
		t.Errorf("filtered empty state should not show capture hint, texts %q", tt.Texts())
	}
}

// TestViewEmptyCategoryFiltered 验证仅分类过滤（无关键字）也走"No matches"。
func TestViewEmptyCategoryFiltered(t *testing.T) {
	h := &harness{filter: FilterImage}
	tt := ui.NewTester(h.presenter().View, 560, 520)
	if !tt.HasText("No matches") {
		t.Errorf("expected filtered empty state, texts %q", tt.Texts())
	}
}

// TestViewEmptyWhitespaceQuery 验证纯空白关键词按"无过滤"处理：
// store 层会 TrimSpace 后视为不过滤，空状态应显示"还没有历史"而非"无匹配结果"。
func TestViewEmptyWhitespaceQuery(t *testing.T) {
	h := &harness{query: "   "}
	tt := ui.NewTester(h.presenter().View, 560, 520)
	if !tt.HasText("Copy some text anywhere") {
		t.Errorf("纯空白查询应显示空历史引导, texts %q", tt.Texts())
	}
	if tt.HasText("No matches") {
		t.Errorf("纯空白查询不应显示无匹配结果, texts %q", tt.Texts())
	}
}

func TestViewLoadingKeepsRowsVisibleAndIgnoresClicks(t *testing.T) {
	h := &harness{
		loading: true,
		items:   []store.Entry{{ID: 7, Kind: store.KindText, Preview: "still visible", CopiedAt: 1700000000000}},
	}
	tt := ui.NewTester(h.presenter().View, 560, 520)
	if !tt.HasText("Loading history...") || !tt.HasText("still visible") {
		t.Fatalf("loading should retain rows and show status, texts=%q", tt.Texts())
	}
	if err := tt.Click("still visible"); err != nil {
		t.Fatal(err)
	}
	if h.hasEvent("activate") {
		t.Fatal("row clicks should be ignored during loading")
	}
}

// TestViewWithItems 验证条目预览与元信息渲染。
func TestViewWithItems(t *testing.T) {
	h := &harness{items: []store.Entry{
		{ID: 1, Kind: store.KindText, Preview: "second entry", CopiedAt: 1700000000000},
		{ID: 2, Kind: store.KindLink, Preview: "https://example.com", CopiedAt: 1700000000000, SourceApp: "Chrome"},
	}}
	tt := ui.NewTester(h.presenter().View, 560, 520)
	if !tt.HasText("second entry") || !tt.HasText("https://example.com") {
		t.Errorf("expected both items, texts %q", tt.Texts())
	}
	if !tt.HasText("2 items") {
		t.Errorf("expected item count, texts %q", tt.Texts())
	}
	// 元信息：来源应用。
	if !tt.HasText("Chrome · ") {
		t.Errorf("expected source app in meta line, texts %q", tt.Texts())
	}
}

// TestViewFavoriteStar 验证收藏星标展示。
func TestViewFavoriteStar(t *testing.T) {
	h := &harness{items: []store.Entry{
		{ID: 1, Kind: store.KindText, Preview: "starred", IsFavorite: true, CopiedAt: 1700000000000},
	}}
	tt := ui.NewTester(h.presenter().View, 560, 520)
	if _, ok := tt.Find("Favorited"); !ok {
		t.Errorf("expected favorite star, texts %q", tt.Texts())
	}
	// 未收藏条目不显示星标。
	b := &harness{items: []store.Entry{
		{ID: 1, Kind: store.KindText, Preview: "plain", CopiedAt: 1700000000000},
	}}
	tt2 := ui.NewTester(b.presenter().View, 560, 520)
	if _, ok := tt2.Find("Favorited"); ok {
		t.Error("unfavorited entry should not show a star")
	}
}

// TestViewStatusMessage 验证状态栏提示优先于快捷键帮助。
func TestViewStatusMessage(t *testing.T) {
	h := &harness{}
	msg := h.locale.T("Copied to clipboard (auto-paste failed, paste manually)")
	h.status = msg
	tt := ui.NewTester(h.presenter().View, 560, 520)
	if !tt.HasText(msg) {
		t.Errorf("expected status message %q, texts %q", msg, tt.Texts())
	}
}

// TestViewSettingsModal 验证设置 Modal：主界面无主题/自启控件，打开后可见。
func TestViewSettingsModal(t *testing.T) {
	// 主界面：标题栏无 Appearance/主题分段，状态栏无 Launch at login。
	h := &harness{themeIdx: 2, langIdx: 0}
	tt := ui.NewTester(h.presenter().View, 560, 520)
	if tt.HasText("Auto") || tt.HasText("Light") || tt.HasText("Dark") {
		t.Errorf("theme switcher should not be on main view, texts %q", tt.Texts())
	}
	if tt.HasText("Launch at login") {
		t.Errorf("autostart should not be on main view, texts %q", tt.Texts())
	}

	// 打开设置：主题标签 + 语言 + 开机自启一并出现（独立 harness，避免 ListState 双绑）。
	b := &harness{themeIdx: 2, langIdx: 0, settingsOpen: true}
	tt2 := ui.NewTester(b.presenter().View, 560, 520)
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
	if err := tt2.Click("Light"); err != nil {
		t.Fatal(err)
	}
	if b.themeIdx != 1 || !b.hasEvent("theme") {
		t.Fatalf("theme action should observe updated binding: index=%d events=%v", b.themeIdx, b.events)
	}
}

// TestSettingsResize 验证缩放后控件仍在面板内，且窄窗口下可以切换主题和关闭。
func TestSettingsResize(t *testing.T) {
	for _, tc := range []struct {
		name string
		lang i18n.Lang
	}{{"en", i18n.LangEN}, {"zh", i18n.LangZH}} {
		t.Run(tc.name, func(t *testing.T) {
			h := &harness{locale: i18n.New(tc.lang), settingsOpen: true, themeIdx: 2}
			p := h.presenter()
			// 单独渲染弹窗，避免主界面同名按钮和页脚遮蔽布局断言。
			tt := ui.NewTester(func(c *ui.Context) { p.settingsModal(c, applyFluentTheme(c)) }, 560, 520)
			tt.SetScale(1.25)
			for _, size := range [][2]int{{560, 520}, {360, 280}, {420, 350}, {640, 570}, {360, 520}, {560, 520}, {360, 280}} {
				tt.SetSize(size[0], size[1])
				tt.Frame()
				panel, ok := tt.Find(h.locale.T("Settings"))
				if !ok || panel.W <= 0 || panel.H <= 0 || panel.X < 0 || panel.Y < 0 || panel.X+panel.W > float32(size[0]) || panel.Y+panel.H > float32(size[1]) {
					t.Fatalf("%v: dialog outside window: %+v", size, panel)
				}
				for _, text := range []string{"Language", "Theme", "Launch at login", "English", "中文", "Auto", "Light", "Dark", "Close"} {
					label := h.locale.T(text)
					el, ok := tt.Find(label)
					if !ok || el.W <= 0 || el.H <= 0 || el.X < panel.X || el.Y < panel.Y || el.X+el.W > panel.X+panel.W || el.Y+el.H > panel.Y+panel.H {
						t.Errorf("%v: setting %q outside dialog %+v: %+v", size, label, panel, el)
					}
				}
			}
			if err := tt.Click(h.locale.T("Light")); err != nil {
				t.Fatal(err)
			}
			if h.themeIdx != 1 || !h.hasEvent("theme") {
				t.Fatalf("theme control unusable after resize: index=%d events=%v", h.themeIdx, h.events)
			}
			if err := tt.Click(h.locale.T("Close")); err != nil {
				t.Fatal(err)
			}
			if h.settingsOpen {
				t.Fatal("dialog should close after resize")
			}
		})
	}
}

// TestViewChineseLanguage 验证切换中文后整窗文案变化。
func TestViewChineseLanguage(t *testing.T) {
	h := &harness{locale: i18n.New(i18n.LangZH)}
	tt := ui.NewTester(h.presenter().View, 560, 520)
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
	b := &harness{settingsOpen: true, locale: i18n.New(i18n.LangZH)}
	tt2 := ui.NewTester(b.presenter().View, 560, 520)
	if !tt2.HasText("设置") || !tt2.HasText("语言") || !tt2.HasText("主题") {
		t.Errorf("expected Chinese settings labels, texts %q", tt2.Texts())
	}
	if !tt2.HasText("自动") || !tt2.HasText("浅色") || !tt2.HasText("暗黑") {
		t.Errorf("expected Chinese theme labels, texts %q", tt2.Texts())
	}
}

// TestFluentHeaderCompact 验证标题栏紧凑：标题在窗口上沿附近，计数与切换器同排。
func TestFluentHeaderCompact(t *testing.T) {
	h := &harness{items: []store.Entry{
		{ID: 1, Kind: store.KindText, Preview: "compact-row", CopiedAt: 1700000000000},
	}}
	tt := ui.NewTester(h.presenter().View, 560, 520)
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

// TestToolbarAndShortcutHintsResize 验证中英文分类和快捷键在窄窗口中完整排布。
func TestToolbarAndShortcutHintsResize(t *testing.T) {
	for _, lang := range []i18n.Lang{i18n.LangEN, i18n.LangZH} {
		h := &harness{locale: i18n.New(lang)}
		tt := ui.NewTester(h.presenter().View, 560, 520)
		labels := h.locale.FilterLabels()
		for _, hint := range shortcutHints {
			labels = append(labels, hint.key, h.locale.T(hint.action))
		}
		for _, size := range [][2]int{{560, 520}, {360, 280}, {560, 520}} {
			tt.SetSize(size[0], size[1])
			for _, label := range labels {
				el, ok := tt.Find(label)
				if !ok || el.W <= 0 || el.H <= 0 || el.X < 0 || el.Y < 0 || el.X+el.W > float32(size[0]) || el.Y+el.H > float32(size[1]) {
					t.Errorf("lang=%v size=%v clips %q: %+v", lang, size, label, el)
				}
			}
		}
	}
}

// TestViewKeyboardNav 验证键盘选择与激活编排（↑↓/Enter）。
func TestViewKeyboardNav(t *testing.T) {
	h := &harness{items: []store.Entry{
		{ID: 1, Kind: store.KindText, Preview: "first", FullText: "first", CopiedAt: 1700000000000},
		{ID: 2, Kind: store.KindText, Preview: "second", FullText: "second", CopiedAt: 1700000000000},
	}}

	tt := ui.NewTester(h.presenter().View, 560, 520)
	tt.Key(0, ui.KeyDown)  // 选中第二条
	tt.Key(0, ui.KeyEnter) // 粘贴选中项

	if h.chosen != 1 {
		t.Fatalf("chosen 应为 1，实际 %d", h.chosen)
	}
	if !h.hasEvent("activate") {
		t.Fatalf("Enter 应触发激活，实际事件 %v", h.events)
	}
}

// TestViewCtrlFFocusesSearch 验证 Ctrl+F 把焦点交给搜索框。
func TestViewCtrlFFocusesSearch(t *testing.T) {
	h := &harness{}
	tt := ui.NewTester(h.presenter().View, 560, 520)
	tt.Key(ui.Cmd, ui.KeyF)
	tt.Frame()
	if !tt.Focused("Search history") {
		t.Errorf("Ctrl+F 后搜索框应获得焦点，texts %q", tt.Texts())
	}
	tt.Type("clipboard")
	if h.query != "clipboard" || !h.hasEvent("search") {
		t.Fatalf("Ctrl+F 后应可直接搜索: query=%q events=%v", h.query, h.events)
	}
	tt.Key(0, ui.KeyEscape)
	if h.query != "" || h.hasEvent("hide") {
		t.Fatalf("Esc 应先清空搜索: query=%q events=%v", h.query, h.events)
	}
	tt.Key(0, ui.KeyEscape)
	if !h.hasEvent("hide") {
		t.Fatal("搜索为空时 Esc 应隐藏窗口")
	}
	tt.Type("another")
	if err := tt.Click("Clear search"); err != nil {
		t.Fatal(err)
	}
	if h.query != "" || !tt.Focused("Search history") {
		t.Fatalf("清空按钮应保留输入焦点: query=%q", h.query)
	}
}

// TestViewCtrlQQuits 验证 Ctrl+Q 派发退出动作。
//
// 关窗只是隐藏窗口（后台常驻捕获），没有退出路径时进程将永远留在后台。
func TestViewCtrlQQuits(t *testing.T) {
	h := &harness{}
	tt := ui.NewTester(h.presenter().View, 560, 520)
	tt.Key(ui.Cmd, ui.KeyQ)
	if !h.hasEvent("quit") {
		t.Errorf("Ctrl+Q 应派发退出动作，事件 %v", h.events)
	}
}

// TestViewKeyboardDeleteAndFavorite 验证 Del / Ctrl+D 派发到对应动作。
func TestViewKeyboardDeleteAndFavorite(t *testing.T) {
	h := &harness{items: []store.Entry{
		{ID: 1, Kind: store.KindText, Preview: "only", CopiedAt: 1700000000000},
	}}
	tt := ui.NewTester(h.presenter().View, 560, 520)
	tt.Key(0, ui.KeyDelete)
	tt.Key(ui.Cmd, ui.KeyD)

	if !h.hasEvent("delete") {
		t.Errorf("Del 应派发删除动作，事件 %v", h.events)
	}
	if !h.hasEvent("favorite") {
		t.Errorf("Ctrl+D 应派发收藏动作，事件 %v", h.events)
	}
}

// TestViewEscapeHidesWindow 验证 Esc 隐藏窗口（设置未打开时）。
func TestViewEscapeHidesWindow(t *testing.T) {
	h := &harness{}
	tt := ui.NewTester(h.presenter().View, 560, 520)
	tt.Key(0, ui.KeyEscape)
	if !h.hasEvent("hide") {
		t.Errorf("Esc 应派发隐藏窗口动作，事件 %v", h.events)
	}
}

// TestViewEscapeClosesSettingsFirst 验证设置打开时 Esc 只关设置、不隐藏窗口。
func TestViewEscapeClosesSettingsFirst(t *testing.T) {
	h := &harness{settingsOpen: true}
	tt := ui.NewTester(h.presenter().View, 560, 520)
	tt.Key(0, ui.KeyEscape)
	if h.hasEvent("hide") {
		t.Errorf("设置打开时 Esc 不应隐藏窗口，事件 %v", h.events)
	}
}

// TestViewClickRowActivates 验证点击条目直接粘贴（不改变选中语义）。
func TestViewClickRowActivates(t *testing.T) {
	h := &harness{items: []store.Entry{
		{ID: 7, Kind: store.KindText, Preview: "clickable", CopiedAt: 1700000000000},
	}}
	tt := ui.NewTester(h.presenter().View, 560, 520)
	if err := tt.Click("clickable"); err != nil {
		t.Fatalf("点击条目失败: %v", err)
	}
	if !h.hasEvent("activate") {
		t.Errorf("点击条目应派发激活动作，事件 %v", h.events)
	}
}

// TestFilterKindMapping 验证分类索引 → 内容类型的映射（全部/收藏不限类型）。
func TestFilterKindMapping(t *testing.T) {
	cases := map[int]store.Kind{
		FilterAll:       "",
		FilterText:      store.KindText,
		FilterLink:      store.KindLink,
		FilterCode:      store.KindCode,
		FilterImage:     store.KindImage,
		FilterFavorites: "",
	}
	for idx, want := range cases {
		if got := FilterKind(idx); got != want {
			t.Errorf("FilterKind(%d) = %q, want %q", idx, got, want)
		}
	}
}
