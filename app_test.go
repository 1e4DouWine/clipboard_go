package main

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"my-go-app/fastpaste"
	"my-go-app/i18n"
	"my-go-app/store"
	"my-go-app/theme"
)

// ---- 测试替身（fake 依赖，验证编排顺序与降级语义）----

// eventLog 记录动作顺序（fakeWin 与 fakePaster 共用）。
//
// 线程安全：搜索防抖的计时器回调运行在后台线程，会经 win.Update 投递刷新，
// 因此共享的事件流必须加锁。
type eventLog struct {
	mu     sync.Mutex
	events []string
}

// add 追加一条事件。
func (l *eventLog) add(s string) {
	l.mu.Lock()
	l.events = append(l.events, s)
	l.mu.Unlock()
}

// list 返回事件快照（拷贝，调用方无需持锁）。
func (l *eventLog) list() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.events...)
}

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
	mu      sync.Mutex
	visible bool
	log     *eventLog // 与 fakePaster 共享事件流，验证调用顺序

	// deferUpdates 为真时 Update 只把回调入队，由测试在自己的 goroutine 上
	// drain 执行——模拟"主线程取用"，避免后台线程直接改测试可见的状态。
	deferUpdates bool
	pending      []func()
	updates      chan struct{}
}

func (w *fakeWin) Update(fn func()) {
	w.mu.Lock()
	if w.deferUpdates {
		notify := len(w.pending) == 0
		w.pending = append(w.pending, fn)
		w.mu.Unlock()
		if notify && w.updates != nil {
			select {
			case w.updates <- struct{}{}:
			default:
			}
		}
		return
	}
	w.mu.Unlock()
	fn()
}

// drain 执行已投递但尚未运行的回调（测试在自己的 goroutine 上调用）。
func (w *fakeWin) drain() {
	for {
		w.mu.Lock()
		pending := w.pending
		w.pending = nil
		w.mu.Unlock()
		if len(pending) == 0 {
			return
		}
		for _, fn := range pending {
			fn()
		}
	}
}

func (w *fakeWin) Show() {
	w.mu.Lock()
	w.visible = true
	w.mu.Unlock()
	w.log.add("show")
}

func (w *fakeWin) Hide() {
	w.mu.Lock()
	w.visible = false
	w.mu.Unlock()
	w.log.add("hide")
}

func (w *fakeWin) Focus() { w.log.add("focus") }

func (w *fakeWin) IsVisible() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.visible
}

func (w *fakeWin) SetTitle(title string) { w.log.add("title:" + title) }

// fakePaster 记录快速粘贴调用；可注入失败。
type fakePaster struct {
	log      *eventLog
	pastes   int
	pasteErr error
}

func (p *fakePaster) Capture() fastpaste.Target {
	p.log.add("capture")
	return fastpaste.Target{}
}

func (p *fakePaster) Paste(t fastpaste.Target) error {
	p.log.add("paste")
	p.pastes++
	return p.pasteErr
}

// fakeQuitter 记录退出请求。
type fakeQuitter struct {
	quits int
}

func (q *fakeQuitter) Quit() { q.quits++ }

// fakeThemeApplier 记录主题应用序列（主题应用回调的测试替身）。
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
	open    bool // 当前自启状态（模拟系统侧）
	failSet bool // 注入 SetOpenAtLogin 失败
}

func (f *fakeLogin) SetOpenAtLogin(open bool) error {
	if f.failSet {
		return errors.New("fake: access denied")
	}
	f.open = open
	return nil
}
func (f *fakeLogin) OpenAtLogin() bool { return f.open }

// newTestApp 创建挂接真实临时 store 与 fake 依赖的 app。
func newTestApp(t *testing.T) (*app, *fakeClip, *fakeWin, *fakePaster) {
	t.Helper()
	st, err := store.Open(t.TempDir(), store.Options{})
	if err != nil {
		t.Fatalf("Open store 失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	log := &eventLog{}
	clip := &fakeClip{}
	win := &fakeWin{log: log, deferUpdates: true, updates: make(chan struct{}, 1)}
	paste := &fakePaster{log: log}
	a := &app{st: st, clip: clip, win: win, paste: paste, quitter: &fakeQuitter{}, queryHistory: st.ListFiltered}
	return a, clip, win, paste
}

// waitForReload runs posted Window.Update callbacks on the test goroutine until
// the latest coalesced list query has completed.
func waitForReload(t *testing.T, a *app) {
	t.Helper()
	win, ok := a.win.(*fakeWin)
	if !ok {
		t.Fatal("waitForReload requires fakeWin")
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		win.drain()
		if !a.loading {
			return
		}
		select {
		case <-win.updates:
		case <-time.After(5 * time.Millisecond):
		}
	}
	t.Fatal("timed out waiting for asynchronous list reload")
}

func reloadAndWait(t *testing.T, a *app) {
	t.Helper()
	a.reload()
	waitForReload(t, a)
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
	reloadAndWait(t, a)
	if len(a.items) != 3 {
		t.Fatalf("全部应为 3 条，实际 %d", len(a.items))
	}

	// 关键字过滤（Brief 查询：结果含预览但不含全文，全文按需读取）。
	a.query = "hello"
	reloadAndWait(t, a)
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
	reloadAndWait(t, a)
	if len(a.items) != 1 || a.items[0].Kind != store.KindLink {
		t.Fatalf("链接分类应 1 条，实际 %+v", a.items)
	}

	// 分类过滤：代码。
	a.filter = filterCode
	reloadAndWait(t, a)
	if len(a.items) != 1 || a.items[0].Kind != store.KindCode {
		t.Fatalf("代码分类应 1 条，实际 %+v", a.items)
	}

	// 收藏过滤：先收藏一条再过滤。
	a.filter = filterAll
	reloadAndWait(t, a)
	if _, err := a.st.ToggleFavorite(a.items[0].ID); err != nil {
		t.Fatalf("ToggleFavorite 失败: %v", err)
	}
	a.filter = filterFavorites
	reloadAndWait(t, a)
	if len(a.items) != 1 || !a.items[0].IsFavorite {
		t.Fatalf("收藏分类应 1 条，实际 %+v", a.items)
	}
}

func TestReloadCoalescesToLatestAndIgnoresStaleResults(t *testing.T) {
	a, _, win, _ := newTestApp(t)
	firstRelease, latestRelease := make(chan struct{}), make(chan struct{})
	started := make(chan string, 3)
	var mu sync.Mutex
	active, maxActive := 0, 0
	a.queryHistory = func(filter store.Filter, _ int) ([]store.Entry, error) {
		mu.Lock()
		active++
		if active > maxActive {
			maxActive = active
		}
		mu.Unlock()
		defer func() {
			mu.Lock()
			active--
			mu.Unlock()
		}()

		started <- filter.Text
		switch filter.Text {
		case "old":
			<-firstRelease
		case "latest":
			<-latestRelease
		}
		return []store.Entry{{Preview: filter.Text}}, nil
	}
	a.items = []store.Entry{{Preview: "retained"}}
	a.query = "old"
	a.reload()
	if got := <-started; got != "old" {
		t.Fatalf("first query = %q, want old", got)
	}

	a.query = "middle"
	a.reload()
	a.query = "latest"
	a.reload()
	close(firstRelease)
	select {
	case <-win.updates:
		win.drain()
	case <-time.After(5 * time.Second):
		t.Fatal("old query did not post its completion")
	}
	select {
	case got := <-started:
		if got != "latest" {
			t.Fatalf("coalesced query = %q, want latest (middle must be skipped)", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("latest query did not start")
	}
	if len(a.items) != 1 || a.items[0].Preview != "retained" {
		t.Fatalf("stale result must not replace retained items, got %+v", a.items)
	}
	if !a.loading {
		t.Fatal("loading should remain true while latest query runs")
	}
	close(latestRelease)
	waitForReload(t, a)
	if len(a.items) != 1 || a.items[0].Preview != "latest" {
		t.Fatalf("latest result was not applied: %+v", a.items)
	}
	mu.Lock()
	gotMaxActive := maxActive
	mu.Unlock()
	if gotMaxActive != 1 {
		t.Fatalf("query workers overlapped: max active=%d", gotMaxActive)
	}
}

func TestLoadingKeepsRowsAndDefersKeyboardActions(t *testing.T) {
	a, clip, _, _ := newTestApp(t)
	addText(t, a, "first row")
	addText(t, a, "second row")
	reloadAndWait(t, a)
	a.chosen = 0

	started, release := make(chan struct{}), make(chan struct{})
	var first sync.Once
	a.queryHistory = func(filter store.Filter, limit int) ([]store.Entry, error) {
		blocked := false
		first.Do(func() { blocked = true })
		if blocked {
			close(started)
			<-release
		}
		return a.st.ListFiltered(filter, limit)
	}
	a.reload()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("query did not start")
	}

	tester := ui.NewTester(a.newPresenter().View, 560, 520)
	if !tester.HasText("Loading history...") {
		t.Fatalf("loading status not rendered, texts=%q", tester.Texts())
	}
	if !tester.HasText("first row") || !tester.HasText("second row") {
		t.Fatalf("existing rows should stay visible during loading, texts=%q", tester.Texts())
	}
	if err := tester.Click("first row"); err != nil {
		t.Fatal(err)
	}
	if len(clip.texts) != 0 || len(a.pendingActions) != 0 {
		t.Fatal("row click should be ignored while loading")
	}
	tester.Key(0, ui.KeyEnter)
	tester.Key(0, ui.KeyDelete)
	tester.Key(ui.Cmd, ui.KeyD)
	if len(a.pendingActions) != 3 {
		t.Fatalf("Enter/Delete/Favorite should wait for the latest query, queued=%v", a.pendingActions)
	}
	if len(clip.texts) != 0 {
		t.Fatal("Enter must not act on stale rows before query completion")
	}

	close(release)
	waitForReload(t, a)
	if len(clip.texts) != 1 {
		t.Fatalf("queued Enter should run after query completion, clipboard=%v", clip.texts)
	}
	items, err := a.st.List(0)
	if err != nil || len(items) != 1 || !items[0].IsFavorite {
		t.Fatalf("queued Delete/Favorite did not apply to latest results: items=%+v err=%v", items, err)
	}
}

func TestFailedLatestReloadDoesNotRunActionsOnStaleRows(t *testing.T) {
	a, clip, _, _ := newTestApp(t)
	addText(t, a, "retained row")
	reloadAndWait(t, a)
	started, release := make(chan struct{}), make(chan struct{})
	a.queryHistory = func(store.Filter, int) ([]store.Entry, error) {
		close(started)
		<-release
		return nil, errors.New("query failed")
	}
	a.reload()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("query did not start")
	}
	a.activate(a.chosen)
	close(release)
	waitForReload(t, a)
	if len(a.items) != 1 || a.items[0].Preview != "retained row" {
		t.Fatalf("query failure should retain displayed rows, got %+v", a.items)
	}
	if len(clip.texts) != 0 || len(a.pendingActions) != 0 {
		t.Fatalf("queued action must be discarded after latest query failure, clipboard=%v pending=%v", clip.texts, a.pendingActions)
	}
	if a.status == "" {
		t.Fatal("query failure should show a status message")
	}
}

func TestMoveChosenClamps(t *testing.T) {
	a, _, _, _ := newTestApp(t)
	addText(t, a, "one")
	addText(t, a, "two")
	reloadAndWait(t, a)

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
	reloadAndWait(t, a)
	if a.chosen != 0 || len(a.items) != 0 {
		t.Fatalf("空列表应 chosen=0，实际 chosen=%d items=%d", a.chosen, len(a.items))
	}
}

// ---- 激活（写回 + 隐藏 + 快速粘贴）----

func TestActivateTextWritesClipboardAndPastes(t *testing.T) {
	a, clip, win, paste := newTestApp(t)
	addText(t, a, "paste me")
	reloadAndWait(t, a)

	a.activate(0)

	if len(clip.texts) != 1 || clip.texts[0] != "paste me" {
		t.Fatalf("应写回文本，实际 %+v", clip.texts)
	}
	if win.IsVisible() {
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
	reloadAndWait(t, a)

	a.activate(0)

	// 降级语义：写回必须成功，粘贴失败仅提示。
	if len(clip.texts) != 1 || clip.texts[0] != "fallback" {
		t.Fatalf("粘贴失败不影响写回，实际 %+v", clip.texts)
	}
	if win.IsVisible() {
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
	reloadAndWait(t, a)

	a.activate(0)

	if len(clip.images) != 1 || string(clip.images[0]) != string(img) {
		t.Fatalf("应写回图片，实际 %+v", clip.images)
	}
}

func TestActivateOutOfRangeIsNoop(t *testing.T) {
	a, clip, _, paste := newTestApp(t)
	reloadAndWait(t, a) // 空列表

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
	reloadAndWait(t, a)

	// 删除选中项（不依赖排序：同毫秒写入的条目顺序不稳定，记录 ID 再比对）。
	deletedID := a.items[a.chosen].ID
	a.deleteChosen()
	waitForReload(t, a)
	if len(a.items) != 1 {
		t.Fatalf("删除后应剩 1 条，实际 %+v", a.items)
	}
	if a.items[0].ID == deletedID {
		t.Fatalf("被删除的条目仍在列表中: %+v", a.items)
	}

	// 收藏切换。
	a.toggleFavoriteChosen()
	waitForReload(t, a)
	if !a.items[0].IsFavorite {
		t.Fatal("应已收藏")
	}
	a.toggleFavoriteChosen()
	waitForReload(t, a)
	if a.items[0].IsFavorite {
		t.Fatal("应已取消收藏")
	}
}

// ---- 窗口唤起与捕获时序 ----

func TestToggleWindowCapturesTargetBeforeShow(t *testing.T) {
	a, _, win, paste := newTestApp(t)

	a.toggleWindow() // 隐藏 → 唤起
	waitForReload(t, a)

	// 关键时序：必须先 capture（记录原前台窗口）再 show。
	events := win.log.list()
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
	if win.IsVisible() {
		t.Fatal("toggleWindow 应在可见时隐藏")
	}
}

func TestOnCapturedRefreshesThroughUpdate(t *testing.T) {
	a, _, _, _ := newTestApp(t)

	// 模拟 watch 后台捕获 → onCaptured 应通过 win.Update 刷新列表。
	addText(t, a, "from background")
	a.onCaptured(store.Entry{})
	waitForReload(t, a)

	if len(a.items) != 1 || a.items[0].Preview != "from background" {
		t.Fatalf("onCaptured 后应刷新列表，实际 %+v", a.items)
	}
}

// ---- 退出路径 ----

// TestQuitRequestsAppQuit 验证 Ctrl+Q 走 quitOps 退出（而不是被窗口 OnClose 挡掉）。
func TestQuitRequestsAppQuit(t *testing.T) {
	a, _, _, _ := newTestApp(t)
	q := &fakeQuitter{}
	a.quitter = q

	a.newPresenter().Actions.Quit()
	if q.quits != 1 {
		t.Fatalf("应请求退出 1 次，实际 %d", q.quits)
	}

}

// TestOnWindowCloseHidesUnlessQuitting 验证关窗语义：
// 正常关闭拦截并隐藏；退出流程中放行真实关闭。
//
// 这是"关窗后进程再也无法退出"的修复点：窗口 OnClose 默认 PreventDefault，
// 会连带挡掉 mygo 的退出流程（App.Quit → prepareQuit → w.close → OnClose）。
func TestOnWindowCloseHidesUnlessQuitting(t *testing.T) {
	a, _, win, _ := newTestApp(t)

	// 正常关闭：拦截 + 隐藏窗口。
	e := &mygo.CloseEvent{}
	a.onWindowClose(e)
	if !e.DefaultPrevented() {
		t.Fatal("正常关闭应被拦截（保持后台运行）")
	}
	if win.IsVisible() {
		t.Fatal("正常关闭应隐藏窗口")
	}

	// 退出流程：放行真实关闭。
	a.markQuitting()
	e2 := &mygo.CloseEvent{}
	a.onWindowClose(e2)
	if e2.DefaultPrevented() {
		t.Fatal("退出流程中应放行真实关闭，否则应用永远退不出去")
	}
}

// ---- 搜索防抖 ----

// TestSearchDebounceDefersReload 验证搜索重载被防抖：
// 连续输入不会每次都查库，停顿时才刷新一次。
func TestSearchDebounceDefersReload(t *testing.T) {
	a, _, win, _ := newTestApp(t)
	win.deferUpdates = true // 让投递的刷新由测试线程执行（模拟主线程）
	a.searchDelay = 30 * time.Millisecond
	addText(t, a, "alpha")
	reloadAndWait(t, a)
	if len(a.items) != 1 {
		t.Fatalf("初始应加载 1 条，实际 %d", len(a.items))
	}

	// 搜索词变化：防抖窗口内不重载。
	a.query = "nomatch"
	a.onSearchChanged()
	if len(a.items) != 1 {
		t.Fatalf("防抖窗口内不应重载，实际 %d 条", len(a.items))
	}
	if !a.searchPending {
		t.Fatal("应标记待生效的搜索重载")
	}

	// 防抖到期：计时器把刷新投递到主线程，执行后按新关键词重载。
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		win.drain()
		if len(a.items) == 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(a.items) != 0 {
		t.Fatalf("防抖到期后应按新关键词重载为 0 条，实际 %d", len(a.items))
	}
	if a.searchPending {
		t.Fatal("重载后不应残留待处理标记")
	}
}

// TestFlushSearchAppliesPendingQuery 验证动作前会 flush 待处理的搜索：
// "按 Enter / Del"作用于当前关键词的结果，与无防抖时同帧刷新的语义一致。
func TestFlushSearchAppliesPendingQuery(t *testing.T) {
	a, _, win, _ := newTestApp(t)
	win.deferUpdates = true
	a.searchDelay = time.Hour // 不会自然到期，只能靠 flush
	addText(t, a, "alpha")
	reloadAndWait(t, a)

	a.query = "nomatch"
	a.onSearchChanged()
	if len(a.items) != 1 {
		t.Fatalf("防抖窗口内不应重载，实际 %d 条", len(a.items))
	}

	if !a.flushSearch() {
		t.Fatal("flushSearch 应报告发生了重载")
	}
	if !a.loading || len(a.items) != 1 {
		t.Fatalf("查询期间应保留旧列表并显示 loading，loading=%v items=%d", a.loading, len(a.items))
	}
	waitForReload(t, a)
	if len(a.items) != 0 {
		t.Fatalf("flush 后应按新关键词重载为 0 条，实际 %d", len(a.items))
	}
	if a.searchPending {
		t.Fatal("flush 后不应残留待处理标记")
	}
	// 无待处理重载时 flush 是 no-op。
	if a.flushSearch() {
		t.Fatal("无待处理重载时 flushSearch 应返回 false")
	}
}

// TestActivateFlushesPendingSearch 验证 Enter 粘贴的是当前关键词的匹配项。
func TestActivateFlushesPendingSearch(t *testing.T) {
	a, clip, win, _ := newTestApp(t)
	win.deferUpdates = true
	a.searchDelay = time.Hour
	addText(t, a, "alpha")
	addText(t, a, "beta")
	reloadAndWait(t, a)
	if len(a.items) != 2 {
		t.Fatalf("初始应加载 2 条，实际 %d", len(a.items))
	}

	// 搜索 "beta" 但防抖未到期：列表仍是旧的 2 条。
	a.query = "beta"
	a.onSearchChanged()
	if len(a.items) != 2 {
		t.Fatalf("防抖窗口内不应重载，实际 %d 条", len(a.items))
	}

	// 激活（Enter）：先 flush 搜索，再按新结果粘贴。
	a.activate(a.chosen)
	waitForReload(t, a)
	if len(clip.texts) != 1 || clip.texts[0] != "beta" {
		t.Fatalf("应粘贴当前关键词的匹配项，实际 %+v", clip.texts)
	}
}

// ---- 分类过滤下推 ----

// TestReloadFilterPushesDown 验证分类过滤在查询层完成（不是先取 N 条再内存过滤）。
func TestReloadFilterPushesDown(t *testing.T) {
	a, _, _, _ := newTestApp(t)
	addText(t, a, "plain text")
	if _, err := a.st.AddImage([]byte("png-bytes"), "paint"); err != nil {
		t.Fatalf("AddImage 失败: %v", err)
	}
	reloadAndWait(t, a)
	if len(a.items) != 2 {
		t.Fatalf("全部应 2 条，实际 %d", len(a.items))
	}

	// 图片分类：只应出现图片条目。
	a.filter = filterImage
	reloadAndWait(t, a)
	if len(a.items) != 1 || a.items[0].Kind != store.KindImage {
		t.Fatalf("图片分类应 1 条，实际 %+v", a.items)
	}

	// 收藏分类：只应出现收藏条目。
	a.filter = filterAll
	reloadAndWait(t, a)
	if _, err := a.st.ToggleFavorite(a.items[0].ID); err != nil {
		t.Fatalf("ToggleFavorite 失败: %v", err)
	}
	a.filter = filterFavorites
	reloadAndWait(t, a)
	if len(a.items) != 1 || !a.items[0].IsFavorite {
		t.Fatalf("收藏分类应 1 条，实际 %+v", a.items)
	}
}

// ---- 开机自启 ----

// TestToggleAutoStart 验证自启开关：生效与失败回滚。
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

}

// ---- 主题切换 ----

// TestSetThemeSwitchesAndPersists 验证切换器动作与主题管理器、存储联动。
func TestSetThemeSwitchesAndPersists(t *testing.T) {
	a, _, _, _ := newTestApp(t)
	ap := &fakeThemeApplier{}
	mgr := theme.New(a.st, ap.Apply)
	a.theming = mgr

	a.setTheme(2) // Dark
	if mgr.Mode() != theme.ModeDark {
		t.Fatalf("应切换到 dark，实际 %q", mgr.Mode())
	}
	if ap.last() != theme.ModeDark {
		t.Fatalf("外观应立即应用 dark，实际 %q", ap.last())
	}
	if value, ok, err := a.st.GetSetting(theme.SettingsKey); err != nil || !ok || value != string(theme.ModeDark) {
		t.Fatalf("主题偏好未保存: value=%q ok=%v err=%v", value, ok, err)
	}

	// 越界序号回退跟随系统（防御性）。
	a.setTheme(99)
	if mgr.Mode() != theme.ModeSystem {
		t.Fatalf("越界应回退 system，实际 %q", mgr.Mode())
	}
}

// TestSetLangSwitchesAndPersists 验证语言切换：a.locale 立即生效 + 写库。
func TestSetLangSwitchesAndPersists(t *testing.T) {
	a, _, _, _ := newTestApp(t)
	a.locale = i18n.New(i18n.LangEN)

	a.setLang(1) // 中文
	if a.locale.Lang() != i18n.LangZH {
		t.Fatalf("应切换到 ZH，实际 %v", a.locale.Lang())
	}
	if a.langIdx != 1 {
		t.Fatalf("langIdx 应为 1，实际 %d", a.langIdx)
	}
	if got := a.t("Clipboard History"); got != "剪贴板历史" {
		t.Fatalf("a.t 应立即返回中文，实际 %q", got)
	}
	if got := i18n.Load(a.st); got != i18n.LangZH {
		t.Fatalf("应持久化 ZH，实际 %v", got)
	}
	// 系统窗口标题随语言同步（SetTitle）。
	foundTitle := false
	for _, e := range a.win.(*fakeWin).log.list() {
		if e == "title:剪贴板历史" {
			foundTitle = true
		}
	}
	if !foundTitle {
		t.Fatalf("应同步窗口标题为中文，events=%v", a.win.(*fakeWin).log.list())
	}

	a.setLang(0) // English
	if a.locale.Lang() != i18n.LangEN || i18n.Load(a.st) != i18n.LangEN {
		t.Fatalf("应回到 EN 并持久化，lang=%v stored=%v", a.locale.Lang(), i18n.Load(a.st))
	}

	// 越界回退英文（防御性）。
	a.setLang(99)
	if a.locale.Lang() != i18n.LangEN || a.langIdx != 0 {
		t.Fatalf("越界应回退 EN，lang=%v idx=%d", a.locale.Lang(), a.langIdx)
	}

}
