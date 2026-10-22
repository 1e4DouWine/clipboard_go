package main

import (
	"errors"
	"testing"

	"github.com/egoist/mygo/ui"

	"my-go-app/fastpaste"
	"my-go-app/store"
	"my-go-app/theme"
)

// ---- 测试替身（fake 依赖，验证编排顺序与降级语义）----

// fakeClip 记录剪贴板写回内容。
type fakeClip struct {
	texts  []string
	images [][]byte
	imgErr error // 注入 WriteImage 失败
}

func (f *fakeClip) WriteText(text string) { f.texts = append(f.texts, text) }
func (f *fakeClip) WriteImage(data []byte) error {
	if f.imgErr != nil {
		return f.imgErr
	}
	f.images = append(f.images, data)
	return nil
}

// fakeWin 记录窗口操作；Update 同步执行模拟主线程投递。
type fakeWin struct {
	visible bool
	events  *[]string // 与 fakePaster 共享事件流，验证调用顺序
}

func (w *fakeWin) Update(fn func()) { fn() }
func (w *fakeWin) Show() {
	w.visible = true
	*w.events = append(*w.events, "show")
}
func (w *fakeWin) Hide() {
	w.visible = false
	*w.events = append(*w.events, "hide")
}
func (w *fakeWin) Focus()          { *w.events = append(*w.events, "focus") }
func (w *fakeWin) IsVisible() bool { return w.visible }
func (w *fakeWin) SetTitle(title string) {
	*w.events = append(*w.events, "title:"+title)
}

// fakePaster 记录快速粘贴调用；可注入失败。
type fakePaster struct {
	events   *[]string
	pastes   int
	pasteErr error
}

func (p *fakePaster) Capture() fastpaste.Target {
	*p.events = append(*p.events, "capture")
	return fastpaste.Target{}
}
func (p *fakePaster) Paste(t fastpaste.Target) error {
	*p.events = append(*p.events, "paste")
	p.pastes++
	return p.pasteErr
}

// fakeThemeApplier 记录主题应用序列（theme.Applier 的测试替身）。
type fakeThemeApplier struct {
	applied []theme.Mode
}

func (f *fakeThemeApplier) Apply(m theme.Mode) { f.applied = append(f.applied, m) }

// last 返回最后一次应用的模式。
func (f *fakeThemeApplier) last() theme.Mode {
	if len(f.applied) == 0 {
		return ""
	}
	return f.applied[len(f.applied)-1]
}

// fakeLogin 记录开机自启状态；可注入设置失败。
type fakeLogin struct {
	open      bool // 当前自启状态（模拟系统侧）
	openedNow bool // 本次是否由登录启动
	failSet   bool // 注入 SetOpenAtLogin 失败
}

func (f *fakeLogin) SetOpenAtLogin(open bool) error {
	if f.failSet {
		return errors.New("fake: access denied")
	}
	f.open = open
	return nil
}
func (f *fakeLogin) OpenAtLogin() bool      { return f.open }
func (f *fakeLogin) WasOpenedAtLogin() bool { return f.openedNow }

// newTestApp 创建挂接真实临时 store 与 fake 依赖的 app。
func newTestApp(t *testing.T) (*app, *fakeClip, *fakeWin, *fakePaster) {
	t.Helper()
	st, err := store.Open(t.TempDir(), store.Options{})
	if err != nil {
		t.Fatalf("Open store 失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	events := &[]string{}
	clip := &fakeClip{}
	win := &fakeWin{events: events}
	paste := &fakePaster{events: events}
	a := &app{st: st, clip: clip, win: win, paste: paste}
	return a, clip, win, paste
}

// addText 是测试便捷方法：写入一条文本历史并返回条目。
func addText(t *testing.T, a *app, text string) store.Entry {
	t.Helper()
	e, err := a.st.AddText(text, "test-app")
	if err != nil {
		t.Fatalf("AddText(%q) 失败: %v", text, err)
	}
	return e
}

// ---- 加载与过滤 ----

func TestReloadSearchAndFilter(t *testing.T) {
	a, _, _, _ := newTestApp(t)
	addText(t, a, "hello world")
	addText(t, a, "https://example.com")
	addText(t, a, "func main() {\n\treturn\n}")

	// 空查询 + 全部：3 条。
	a.query, a.filter = "", filterAll
	a.reload()
	if len(a.items) != 3 {
		t.Fatalf("全部应为 3 条，实际 %d", len(a.items))
	}

	// 关键字过滤（Brief 查询：结果含预览但不含全文，全文按需读取）。
	a.query = "hello"
	a.reload()
	if len(a.items) != 1 || a.items[0].Preview != "hello world" {
		t.Fatalf("搜索 hello 应 1 条，实际 %+v", a.items)
	}
	if a.items[0].FullText != "" {
		t.Fatal("列表查询不应加载全文（内存优化约定）")
	}
	// 全文按需获取（writeBack 的取数路径）。
	full, err := a.st.Get(a.items[0].ID)
	if err != nil || full.FullText != "hello world" {
		t.Fatalf("按需取全文失败: %+v err=%v", full, err)
	}

	// 分类过滤：链接。
	a.query, a.filter = "", filterLink
	a.reload()
	if len(a.items) != 1 || a.items[0].Kind != store.KindLink {
		t.Fatalf("链接分类应 1 条，实际 %+v", a.items)
	}

	// 分类过滤：代码。
	a.filter = filterCode
	a.reload()
	if len(a.items) != 1 || a.items[0].Kind != store.KindCode {
		t.Fatalf("代码分类应 1 条，实际 %+v", a.items)
	}

	// 收藏过滤：先收藏一条再过滤。
	a.filter = filterAll
	a.reload()
	if _, err := a.st.ToggleFavorite(a.items[0].ID); err != nil {
		t.Fatalf("ToggleFavorite 失败: %v", err)
	}
	a.filter = filterFavorites
	a.reload()
	if len(a.items) != 1 || !a.items[0].IsFavorite {
		t.Fatalf("收藏分类应 1 条，实际 %+v", a.items)
	}
}

func TestMoveChosenClamps(t *testing.T) {
	a, _, _, _ := newTestApp(t)
	addText(t, a, "one")
	addText(t, a, "two")
	a.reload()

	a.moveChosen(-1) // 越界向上 → 钳制到 0
	if a.chosen != 0 {
		t.Fatalf("chosen 应为 0，实际 %d", a.chosen)
	}
	a.moveChosen(1)
	a.moveChosen(1) // 越界向下 → 钳制到最后一条
	if a.chosen != 1 {
		t.Fatalf("chosen 应为 1，实际 %d", a.chosen)
	}

	// 清空后 chosen 归零。
	a.query = "no-such-entry"
	a.reload()
	if a.chosen != 0 || len(a.items) != 0 {
		t.Fatalf("空列表应 chosen=0，实际 chosen=%d items=%d", a.chosen, len(a.items))
	}
}

// ---- 激活（写回 + 隐藏 + 快速粘贴）----

func TestActivateTextWritesClipboardAndPastes(t *testing.T) {
	a, clip, win, paste := newTestApp(t)
	addText(t, a, "paste me")
	a.reload()

	a.activate(0)

	if len(clip.texts) != 1 || clip.texts[0] != "paste me" {
		t.Fatalf("应写回文本，实际 %+v", clip.texts)
	}
	if win.visible {
		t.Fatal("激活后窗口应隐藏")
	}
	if paste.pastes != 1 {
		t.Fatalf("应执行 1 次快速粘贴，实际 %d", paste.pastes)
	}
}

func TestActivatePasteFailureKeepsClipboard(t *testing.T) {
	a, clip, win, paste := newTestApp(t)
	paste.pasteErr = errors.New("target window gone")
	addText(t, a, "fallback")
	a.reload()

	a.activate(0)

	// 降级语义：写回必须成功，粘贴失败仅提示。
	if len(clip.texts) != 1 || clip.texts[0] != "fallback" {
		t.Fatalf("粘贴失败不影响写回，实际 %+v", clip.texts)
	}
	if win.visible {
		t.Fatal("窗口仍应隐藏（内容已在剪贴板）")
	}
	if a.status == "" {
		t.Fatal("粘贴失败应给出状态提示")
	}
}

func TestActivateImage(t *testing.T) {
	a, clip, _, _ := newTestApp(t)
	img := []byte("png-bytes")
	if _, err := a.st.AddImage(img, "paint"); err != nil {
		t.Fatalf("AddImage 失败: %v", err)
	}
	a.reload()

	a.activate(0)

	if len(clip.images) != 1 || string(clip.images[0]) != string(img) {
		t.Fatalf("应写回图片，实际 %+v", clip.images)
	}
}

func TestActivateOutOfRangeIsNoop(t *testing.T) {
	a, clip, _, paste := newTestApp(t)
	a.reload() // 空列表

	a.activate(0)
	a.activate(-1)

	if len(clip.texts) != 0 || paste.pastes != 0 {
		t.Fatal("越界激活不应有任何副作用")
	}
}

// ---- 删除与收藏 ----

func TestDeleteAndFavorite(t *testing.T) {
	a, _, _, _ := newTestApp(t)
	addText(t, a, "first")
	addText(t, a, "second")
	a.reload()

	// 删除选中项（不依赖排序：同毫秒写入的条目顺序不稳定，记录 ID 再比对）。
	deletedID := a.items[a.chosen].ID
	a.deleteChosen()
	if len(a.items) != 1 {
		t.Fatalf("删除后应剩 1 条，实际 %+v", a.items)
	}
	if a.items[0].ID == deletedID {
		t.Fatalf("被删除的条目仍在列表中: %+v", a.items)
	}

	// 收藏切换。
	a.toggleFavoriteChosen()
	a.reload()
	if !a.items[0].IsFavorite {
		t.Fatal("应已收藏")
	}
	a.toggleFavoriteChosen()
	a.reload()
	if a.items[0].IsFavorite {
		t.Fatal("应已取消收藏")
	}
}

// ---- 窗口唤起与捕获时序 ----

func TestToggleWindowCapturesTargetBeforeShow(t *testing.T) {
	a, _, win, paste := newTestApp(t)

	a.toggleWindow() // 隐藏 → 唤起

	// 关键时序：必须先 capture（记录原前台窗口）再 show。
	events := *win.events
	idxCapture, idxShow := -1, -1
	for i, e := range events {
		switch e {
		case "capture":
			idxCapture = i
		case "show":
			idxShow = i
		}
	}
	if idxCapture < 0 || idxShow < 0 || idxCapture > idxShow {
		t.Fatalf("capture 必须在 show 之前，事件流: %v", events)
	}
	if paste.pastes != 0 {
		t.Fatal("唤起不应触发粘贴")
	}

	// 再次唤起切换为隐藏。
	a.toggleWindow()
	if win.visible {
		t.Fatal("toggleWindow 应在可见时隐藏")
	}
}

func TestOnCapturedRefreshesThroughUpdate(t *testing.T) {
	a, _, _, _ := newTestApp(t)

	// 模拟 watch 后台捕获 → onCaptured 应通过 win.Update 刷新列表。
	addText(t, a, "from background")
	a.onCaptured(store.Entry{})

	if len(a.items) != 1 || a.items[0].Preview != "from background" {
		t.Fatalf("onCaptured 后应刷新列表，实际 %+v", a.items)
	}
}

// ---- 开机自启 ----

// TestToggleAutoStart 验证自启开关：生效、失败回滚、nil 防御。
func TestToggleAutoStart(t *testing.T) {
	a, _, _, _ := newTestApp(t)
	login := &fakeLogin{}
	a.login = login

	// 开启：系统状态与 UI 同步。
	a.toggleAutoStart(true)
	if !login.open || !a.autoStart {
		t.Fatalf("应开启自启: sys=%v ui=%v", login.open, a.autoStart)
	}

	// 关闭。
	a.toggleAutoStart(false)
	if login.open || a.autoStart {
		t.Fatalf("应关闭自启: sys=%v ui=%v", login.open, a.autoStart)
	}

	// 失败回滚：UI 回到系统真实状态并提示，不留"假开关"。
	login.open = true // 系统侧当前是开启
	a.autoStart = false
	login.failSet = true
	a.toggleAutoStart(false) // 设置失败
	if a.autoStart != true {
		t.Fatalf("设置失败应回滚 UI 为系统状态 true，实际 %v", a.autoStart)
	}
	if a.status == "" {
		t.Fatal("设置失败应给出状态提示")
	}

	// nil login 防御（测试环境）。
	a.login = nil
	a.toggleAutoStart(true) // 不应 panic
}

// ---- 主题切换 ----

// TestSetThemeSwitchesAndNotifies 验证切换器动作与主题管理器联动。
func TestSetThemeSwitchesAndNotifies(t *testing.T) {
	a, _, _, _ := newTestApp(t)
	ap := &fakeThemeApplier{}
	mgr, err := theme.New(a.st, ap)
	if err != nil {
		t.Fatalf("theme.New 失败: %v", err)
	}
	a.theming = mgr

	a.setTheme(2) // Dark
	if mgr.Mode() != theme.ModeDark {
		t.Fatalf("应切换到 dark，实际 %q", mgr.Mode())
	}
	if ap.last() != theme.ModeDark {
		t.Fatalf("外观应立即应用 dark，实际 %q", ap.last())
	}

	// 越界序号回退跟随系统（防御性）。
	a.setTheme(99)
	if mgr.Mode() != theme.ModeSystem {
		t.Fatalf("越界应回退 system，实际 %q", mgr.Mode())
	}

	// 无主题管理器时静默忽略（测试环境）。
	a.theming = nil
	a.setTheme(1) // 不应 panic
}

// TestSetLangSwitchesAndPersists 验证语言切换：全局 lang 立即生效 + 写库。
func TestSetLangSwitchesAndPersists(t *testing.T) {
	lang = LangEN
	defer func() { lang = LangEN }()

	a, _, _, _ := newTestApp(t)

	a.setLang(1) // 中文
	if lang != LangZH {
		t.Fatalf("应切换到 ZH，实际 %v", lang)
	}
	if a.langIdx != 1 {
		t.Fatalf("langIdx 应为 1，实际 %d", a.langIdx)
	}
	if got := T("Clipboard History"); got != "剪贴板历史" {
		t.Fatalf("T 应立即返回中文，实际 %q", got)
	}
	if got := LoadLang(a.st); got != LangZH {
		t.Fatalf("应持久化 ZH，实际 %v", got)
	}
	// 系统窗口标题随语言同步（SetTitle）。
	foundTitle := false
	for _, e := range *a.win.(*fakeWin).events {
		if e == "title:剪贴板历史" {
			foundTitle = true
		}
	}
	if !foundTitle {
		t.Fatalf("应同步窗口标题为中文，events=%v", *a.win.(*fakeWin).events)
	}

	a.setLang(0) // English
	if lang != LangEN || LoadLang(a.st) != LangEN {
		t.Fatalf("应回到 EN 并持久化，lang=%v stored=%v", lang, LoadLang(a.st))
	}

	// 越界回退英文（防御性）。
	a.setLang(99)
	if lang != LangEN || a.langIdx != 0 {
		t.Fatalf("越界应回退 EN，lang=%v idx=%d", lang, a.langIdx)
	}

	// 无 store 时只切内存（测试环境）。
	a.st = nil
	a.setLang(1)
	if lang != LangZH {
		t.Fatalf("无 store 仍应切换 lang，实际 %v", lang)
	}
}

// ---- 纯函数 ----

func TestKindBadgeAndMeta(t *testing.T) {
	if got := kindBadge(store.KindImage); got != "IMG" {
		t.Fatalf("kindBadge(IMG) = %q", got)
	}
	if got := kindBadge(store.KindText); got != "TXT" {
		t.Fatalf("kindBadge(TXT) = %q", got)
	}
	e := store.Entry{CopiedAt: 1700000000000, SourceApp: "VS Code"}
	if got := metaLine(e); got == "" {
		t.Fatal("metaLine 不应为空")
	}
}

// TestChipColors 验证类型徽标配色策略（语义色 + 半透明底，明暗主题自动适配）。
func TestChipColors(t *testing.T) {
	th := ui.LightTheme()
	// URL 用 Accent、CODE 用 Warning、IMG 用 Success、TXT 用 TextMuted。
	cases := map[store.Kind]ui.Color{
		store.KindLink:  th.Accent,
		store.KindCode:  th.Warning,
		store.KindImage: th.Success,
		store.KindText:  th.TextMuted,
	}
	for kind, want := range cases {
		fg, bg := chipColors(th, kind)
		if fg != want {
			t.Errorf("chipColors(%q) 前景色 = %v，期望 %v", kind, fg, want)
		}
		if bg.A == 0 {
			t.Errorf("chipColors(%q) 背景色不应全透明", kind)
		}
	}
	// 暗黑主题下同样返回非零配色（自动适配验证）。
	dark := ui.DarkTheme()
	if fg, bg := chipColors(dark, store.KindLink); fg != dark.Accent || bg.A == 0 {
		t.Error("暗黑主题下 chip 配色未适配")
	}
}
