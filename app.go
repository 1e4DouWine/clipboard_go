package main

import (
	"context"
	"log"
	"time"

	"github.com/egoist/mygo"

	"my-go-app/fastpaste"
	"my-go-app/hotkey"
	"my-go-app/i18n"
	"my-go-app/store"
	"my-go-app/theme"
	"my-go-app/uiapp"
)

// maxVisible 是列表一次加载的最大条数（过滤在 SQL 层完成，LIMIT 作用于过滤后的结果）。
const maxVisible = 500

// defaultSearchDebounce 是搜索输入的防抖窗口：
// 连续输入时只在停顿后重查一次，避免每个按键都触发一次全表扫描。
const defaultSearchDebounce = 150 * time.Millisecond

// filter* 是分类过滤器索引（定义在 uiapp：与 ui.Segmented 选项一一对应，
// 标签见 i18n.Locale.FilterLabels）。此处别名以便业务层引用。
const (
	filterAll       = uiapp.FilterAll
	filterText      = uiapp.FilterText
	filterLink      = uiapp.FilterLink
	filterCode      = uiapp.FilterCode
	filterImage     = uiapp.FilterImage
	filterFavorites = uiapp.FilterFavorites
)

// ---- 依赖接口（生产实现见 main.go，测试注入 fake）----

// clipWriter 是剪贴板写回能力的最小依赖。
// mygo.Clipboard 的方法集天然满足本接口。
type clipWriter interface {
	WriteText(text string)
	WriteImage(data []byte) error
}

// windowOps 是窗口操作的最小依赖。*mygo.Window 天然满足本接口。
type windowOps interface {
	Update(fn func())
	Show()
	Hide()
	Focus()
	IsVisible() bool
	SetTitle(title string) // 系统窗口标题（语言切换时同步）
}

// paster 是快速粘贴能力的最小依赖（见 fastpaste 包）。
type paster interface {
	Capture() fastpaste.Target
	Paste(t fastpaste.Target) error
}

// loginOps 是开机自启能力的最小依赖。
// mygo.App 的方法集天然满足本接口，测试注入 fake。
type loginOps interface {
	SetOpenAtLogin(open bool) error // 设置开机自启，失败返回 error
	OpenAtLogin() bool              // 查询当前是否开机自启
	WasOpenedAtLogin() bool         // 本次是否由登录启动（静默后台判定）
}

// quitOps 是退出应用能力的最小依赖。
// mygo.App 的方法集天然满足本接口，测试注入 fake。
type quitOps interface {
	Quit() // 关闭全部窗口并退出进程
}

// fastPaster 是 paster 的生产实现，桥接 fastpaste 包函数。
type fastPaster struct{}

func (fastPaster) Capture() fastpaste.Target      { return fastpaste.Capture() }
func (fastPaster) Paste(t fastpaste.Target) error { return fastpaste.Paste(t) }

type reloadRequest struct {
	generation uint64
	filter     store.Filter
}

type deferredListAction uint8

const (
	deferredActivate deferredListAction = iota + 1
	deferredDelete
	deferredToggleFavorite
)

// app 是应用核心：持有持久化与依赖，编排捕获/列表/搜索/回写/快速粘贴。
//
// 线程约定（遵守 mygo 规则）：
//   - 除 onCaptured 外，所有字段只在主线程（view 构建与动作回调）读写；
//   - onCaptured 在 watch 后台 goroutine 触发，仅通过 win.Update 投递主线程。
type app struct {
	st      *store.Store
	clip    clipWriter
	win     windowOps
	paste   paster
	theming *theme.Manager  // 主题管理
	login   loginOps        // 开机自启
	quitter quitOps         // 退出应用
	hotkeys *hotkey.Manager // 全局快捷键管理器（退出时归还）

	// watchCancel 停止后台剪贴板监听（初始化失败或退出时调用）。
	watchCancel context.CancelFunc
	watchDone   <-chan struct{} // 监听 goroutine 已完全退出

	// ── UI 状态（主线程）──
	items        []store.Entry    // 当前展示的条目（搜索 + 分类过滤后）
	query        string           // 搜索框绑定值
	filter       int              // 分类过滤索引（uiapp.FilterAll..uiapp.FilterFavorites）
	chosen       int              // 选中条目索引
	themeIdx     int              // 主题切换器绑定值（对应 theme.Mode.Index()）
	langIdx      int              // 语言切换器绑定值（对应 i18n.Lang.Index()）
	locale       i18n.Locale      // 当前界面语言（值类型，无全局状态）
	settingsOpen bool             // 设置 Modal 是否打开
	autoStart    bool             // 开机自启开关绑定值（启动时从系统状态同步）
	status       string           // 状态栏提示（错误降级、操作反馈）
	prevTarget   fastpaste.Target // 快速粘贴目标窗口（唤起前捕获）
	quitting     bool             // 正在退出（放行窗口真实关闭）

	// ── 搜索防抖 ──
	// 状态字段只在主线程读写；计时器回调在后台线程，到期后经 win.Update
	// 投递回主线程再触碰这些字段。
	searchPending bool          // 是否有待生效的搜索重载
	searchTimer   *time.Timer   // 防抖计时器
	searchDelay   time.Duration // 防抖窗口；<=0 表示立即重载（测试用）
	searchGen     uint64        // 使已排队的旧计时器回调失效

	reloadGen      uint64
	reloadInFlight bool
	pendingReload  *reloadRequest
	loading        bool
	pendingActions []deferredListAction
	queryHistory   func(store.Filter, int) ([]store.Entry, error) // 测试可注入查询

	ui *uiapp.Presenter // 视图（渲染与交互，持有控件状态）
}

// ---- 数据加载与过滤 ----

// reload asynchronously reloads the list and applies only the newest result.
//
// 内存优化：列表走 Brief 查询（不加载全文），避免大批量全文常驻内存；
// 全文在粘贴回写时按需 Get(id) 读取。
//
// 过滤下推：搜索词、分类、收藏三个条件一并交给 store 在 SQL 层完成，
// LIMIT 作用于过滤后的结果——不会出现"最新 500 条里没有图片，
// 于是更早的图片也永远看不到"的情况。
//
// 慢查询在后台执行；结果只在仍是最新请求时应用到 UI。
func (a *app) reload() {
	if a.quitting {
		return
	}
	a.stopSearchTimer()
	a.reloadGen++
	req := &reloadRequest{generation: a.reloadGen, filter: store.Filter{
		Text:          a.query,
		Kind:          uiapp.FilterKind(a.filter),
		FavoritesOnly: a.filter == filterFavorites,
	}}
	a.loading = true
	if a.reloadInFlight {
		a.pendingReload = req
		return
	}
	a.startReload(*req)
}

func (a *app) startReload(req reloadRequest) {
	a.reloadInFlight = true
	a.loading = true
	st, win, query := a.st, a.win, a.queryHistory
	go func() {
		var entries []store.Entry
		var err error
		if query != nil {
			entries, err = query(req.filter, maxVisible)
		} else {
			entries, err = st.ListFiltered(req.filter, maxVisible)
		}
		win.Update(func() { a.finishReload(req, entries, err) })
	}()
}

func (a *app) finishReload(req reloadRequest, entries []store.Entry, err error) {
	a.reloadInFlight = false
	if a.quitting {
		a.pendingReload = nil
		a.pendingActions = nil
		a.loading = false
		return
	}
	latestQueryFailed := req.generation == a.reloadGen && err != nil
	if req.generation == a.reloadGen {
		if err != nil {
			log.Printf("加载历史失败: %v", err)
			a.status = a.t("Failed to load history. Check the log.")
		} else {
			a.items = entries
			a.clampChosen()
		}
	}
	if a.pendingReload != nil {
		next := *a.pendingReload
		a.pendingReload = nil
		a.startReload(next)
		return
	}
	a.loading = false
	if latestQueryFailed {
		a.pendingActions = nil
		return
	}
	a.runPendingActions()
}

func (a *app) runPendingActions() {
	for len(a.pendingActions) > 0 && !a.loading {
		action := a.pendingActions[0]
		a.pendingActions = a.pendingActions[1:]
		switch action {
		case deferredActivate:
			a.activate(a.chosen)
		case deferredDelete:
			a.deleteChosen()
		case deferredToggleFavorite:
			a.toggleFavoriteChosen()
		}
	}
}

func (a *app) deferListAction(action deferredListAction) {
	a.pendingActions = append(a.pendingActions, action)
}

// waitForLatest defers list actions until the current query and any newer
// coalesced query have completed.
func (a *app) waitForLatest(action deferredListAction) bool {
	if !a.searchPending && !a.loading && !a.reloadInFlight && a.pendingReload == nil {
		return false
	}
	a.deferListAction(action)
	if a.searchPending {
		a.flushSearch()
	}
	return true
}

// clampChosen 把选中索引收敛到合法范围（列表变短/清空时不越界）。
func (a *app) clampChosen() {
	if len(a.items) == 0 {
		a.chosen = 0
		return
	}
	if a.chosen >= len(a.items) {
		a.chosen = len(a.items) - 1
	}
	if a.chosen < 0 {
		a.chosen = 0
	}
}

// moveChosen 上下移动选中项，越界钳制。
//
// 只维护选中索引（业务状态）；列表滚动由视图负责（控件状态），
// 视图在命令生效后读回新索引再滚动。
func (a *app) moveChosen(delta int) {
	a.chosen += delta
	a.clampChosen()
}

// ---- 条目操作 ----

// activate 选中条目"粘贴"：写回剪贴板 → 隐藏窗口 → 快速粘贴到原窗口。
//
// 降级语义：写回是保底动作，必须先成功；快速粘贴失败（目标窗口关闭、
// 被系统拒绝等）只提示用户手动粘贴，不影响已完成的写回。
func (a *app) activate(i int) {
	if a.waitForLatest(deferredActivate) {
		return
	}
	if i < 0 || i >= len(a.items) {
		return // 边界：列表为空或索引越界
	}
	a.chosen = i // 激活的即选中的（点击路径保证高亮与操作一致）
	e := a.items[i]

	if err := a.writeBack(e); err != nil {
		log.Printf("写回剪贴板失败: %v", err)
		a.status = a.tpl("Failed to write clipboard: %s", err.Error())
		return
	}

	// 先隐藏再注入：让出前台焦点，按键才能落到目标窗口。
	a.win.Hide()
	if err := a.paste.Paste(a.prevTarget); err != nil {
		// 注入失败是可预期故障（见 fastpaste 文档）：提示手动粘贴。
		a.status = a.t("Copied to clipboard (auto-paste failed, paste manually)")
		log.Printf("快速粘贴失败: %v", err)
	}
}

// writeBack 把条目写回系统剪贴板：文本走 WriteText，图片走 ReadImage+WriteImage。
//
// 内存优化：列表条目不含全文（Brief 查询），文本回写时按需 Get(id) 取全文，
// 写完即释放引用——大文本不会长期驻留在 UI 状态里。
func (a *app) writeBack(e store.Entry) error {
	if e.Kind == store.KindImage {
		data, err := a.st.ReadImage(e)
		if err != nil {
			return err // 图片文件丢失等，提示用户删除该条目
		}
		return a.clip.WriteImage(data)
	}
	// 文本条目：FullText 为空说明来自 Brief 查询，按需取全文。
	text := e.FullText
	if text == "" {
		full, err := a.st.Get(e.ID)
		if err != nil {
			return err // 条目可能刚被淘汰/删除
		}
		text = full.FullText
	}
	a.clip.WriteText(text)
	return nil
}

// deleteChosen 删除选中条目并刷新列表。
func (a *app) deleteChosen() {
	if a.waitForLatest(deferredDelete) {
		return
	}
	if a.chosen < 0 || a.chosen >= len(a.items) {
		return
	}
	id := a.items[a.chosen].ID
	if err := a.st.Delete(id); err != nil {
		log.Printf("删除条目失败: %v", err)
		a.status = a.tpl("Failed to delete: %s", err.Error())
		return
	}
	a.reload()
}

// toggleFavoriteChosen 切换选中条目的收藏标记。
func (a *app) toggleFavoriteChosen() {
	if a.waitForLatest(deferredToggleFavorite) {
		return
	}
	if a.chosen < 0 || a.chosen >= len(a.items) {
		return
	}
	if _, err := a.st.ToggleFavorite(a.items[a.chosen].ID); err != nil {
		log.Printf("切换收藏失败: %v", err)
		a.status = a.tpl("Action failed: %s", err.Error())
		return
	}
	a.reload()
}

// ---- 开机自启 ----

// toggleAutoStart 设置开机自启（设置页开关回调，主线程执行）。
//
// 错误语义：设置失败（权限不足、不支持的环境等）时回滚开关状态
// 并提示用户——UI 永远反映系统真实状态，不留"假开关"。
func (a *app) toggleAutoStart(on bool) {
	if err := a.login.SetOpenAtLogin(on); err != nil {
		log.Printf("设置开机自启失败: %v", err)
		a.autoStart = a.login.OpenAtLogin() // 回滚为系统实际状态
		a.status = a.t("Failed to enable launch at login. Check system permissions.")
		return
	}
	a.autoStart = on // 成功：UI 与系统一致（幂等写入，Checkbox 绑定值同源）
	a.status = ""
}

// ---- 主题切换 ----

// setTheme 切换外观模式（设置页切换器回调，主线程执行）。
//
// 语义：外观立即生效（mygo.Theme.SetSource 热切换）；
// 偏好持久化失败仅提示，不影响本次切换。
func (a *app) setTheme(idx int) {
	a.themeIdx = idx
	mode := theme.ModeOfIndex(idx)
	if err := a.theming.SetMode(mode); err != nil {
		log.Printf("主题切换: %v", err)
		a.status = a.t("Appearance changed (save failed; will reset after restart)")
		return
	}
	a.status = ""
}

// setLang 切换界面语言（设置页切换器回调，主线程执行）。
//
// 语义：文案立即生效（a.locale 换新值）；持久化失败仅提示。
func (a *app) setLang(idx int) {
	l := i18n.LangOfIndex(idx)
	a.locale = i18n.New(l)
	a.langIdx = a.locale.Index()
	a.locale.ApplyMygo() // 同步控件 chrome（Search 占位符等）
	a.syncWindowTitle()
	if err := i18n.Save(a.st, l); err != nil {
		log.Printf("保存语言偏好失败: %v", err)
		a.status = a.t("Language changed (save failed; will reset after restart)")
		return
	}
	a.status = ""
}

// t 返回当前语言下的文案（状态提示与标题共用）。
func (a *app) t(en string) string { return a.locale.T(en) }

// tpl 返回当前语言下的格式化文案。
func (a *app) tpl(en string, args ...any) string { return a.locale.Tpl(en, args...) }

// syncWindowTitle 把系统窗口标题同步到当前语言。
// App.SetName 保持产品标识不变（改名会迁移数据目录），只动窗口标题。
func (a *app) syncWindowTitle() {
	a.win.SetTitle(a.t("Clipboard History"))
}

// ---- 窗口与全局快捷键 ----

// toggleWindow 唤起/隐藏主窗口（全局快捷键回调，主线程执行）。
//
// 快速粘贴的关键时序：唤起前（前台仍是用户的原窗口）先 Capture 目标，
// 粘贴时才能回填到正确窗口。
func (a *app) toggleWindow() {
	if a.win.IsVisible() {
		a.win.Hide()
		return
	}
	a.prevTarget = a.paste.Capture() // 必须在 Show 之前捕获
	a.reload()                       // 每次唤起刷新，反映最新历史
	a.status = ""
	a.win.Show()
	a.win.Focus()
}

// onCaptured 由 watch 在后台 goroutine 调用：只做一件事——把刷新投递主线程。
// （win.Update 是异步的，绝不能在这里直接改 UI 状态。）
func (a *app) onCaptured(e store.Entry) {
	a.win.Update(func() {
		a.reload()
	})
}

// ---- 视图接线 ----

// snapshot 返回视图渲染所需的只读状态快照。
func (a *app) snapshot() uiapp.State {
	return uiapp.State{
		Items:   a.items,
		Chosen:  a.chosen,
		Status:  a.status,
		Loading: a.loading,
		Locale:  a.locale,
	}
}

// newPresenter 装配视图：状态快照 + 控件绑定指针 + 命令回调。
//
// 绑定指针直接指向模型字段，控件写入即写模型，视图不持有第二份副本。
func (a *app) newPresenter() *uiapp.Presenter {
	return uiapp.New(a.snapshot, uiapp.Config{
		Query:        &a.query,
		Filter:       &a.filter,
		ThemeIdx:     &a.themeIdx,
		LangIdx:      &a.langIdx,
		AutoStart:    &a.autoStart,
		SettingsOpen: &a.settingsOpen,
	}, uiapp.Actions{
		SearchChanged:   a.onSearchChanged,
		FilterChanged:   a.reload,
		MoveChosen:      a.moveChosen,
		Activate:        a.activate,
		DeleteChosen:    a.deleteChosen,
		ToggleFavorite:  a.toggleFavoriteChosen,
		ToggleAutoStart: a.toggleAutoStart,
		ThemeChanged:    a.setTheme,
		LangChanged:     a.setLang,
		HideWindow:      a.hideWindow,
		Quit:            a.quit,
	})
}

// hideWindow 隐藏主窗口（Esc）。
func (a *app) hideWindow() {
	a.win.Hide()
}

// onSearchChanged 搜索词变化（视图回调，主线程执行）。
//
// 重载被防抖：连续输入时只在停顿后查一次库，避免每个按键都触发一次
// 全表 LIKE 扫描（历史量大时一次扫描可达百 MB 级文本）。
// 到期后的刷新经 win.Update 投递主线程，不在后台线程碰 UI 状态。
func (a *app) onSearchChanged() {
	a.searchPending = true
	a.searchGen++
	gen := a.searchGen
	if a.searchDelay <= 0 {
		a.searchPending = false
		a.reload()
		return
	}
	if a.searchTimer != nil {
		a.searchTimer.Stop()
	}
	a.searchTimer = time.AfterFunc(a.searchDelay, func() { a.onSearchTimer(gen) })
}

// onSearchTimer 是防抖到期回调（后台 goroutine）：把刷新投递主线程。
func (a *app) onSearchTimer(gen uint64) {
	a.win.Update(func() {
		if gen != a.searchGen || !a.searchPending {
			return
		}
		a.searchPending = false
		a.reload()
	})
}

// flushSearch 立即执行待处理的搜索重载，返回是否真的重载了。
//
// 作用：让"按 Enter / Del / Ctrl+D"作用于当前关键词的结果，
// 与无防抖时"同帧刷新"的语义一致。
func (a *app) flushSearch() bool {
	if !a.searchPending {
		return false
	}
	a.searchPending = false
	if a.searchTimer != nil {
		a.searchTimer.Stop()
	}
	a.reload()
	return true
}

// stopSearchTimer 停止防抖计时器（应用退出时调用，避免退出后仍触发刷新）。
func (a *app) stopSearchTimer() {
	a.searchGen++
	if a.searchTimer != nil {
		a.searchTimer.Stop()
		a.searchTimer = nil
	}
	a.searchPending = false
}

// quit 退出应用（视图回调，主线程执行）。
func (a *app) quit() {
	a.quitter.Quit()
}

// markQuitting 标记应用正在退出（OnBeforeQuit 回调，主线程执行）。
//
// 必要性：窗口的 OnClose 默认"隐藏而非关闭"，会连带挡掉 mygo 的退出流程
// （App.Quit → prepareQuit → w.close → OnClose → PreventDefault）。
// 退出流程中必须放行真实关闭。
func (a *app) markQuitting() {
	a.quitting = true
	a.stopSearchTimer()
	a.pendingReload = nil
	a.pendingActions = nil
	a.loading = false
}

// onWindowClose 处理窗口关闭：
//   - 正常关闭：拦截并隐藏窗口，后台继续捕获剪贴板；
//   - 退出流程：放行真实关闭，让应用真正退出。
func (a *app) onWindowClose(e *mygo.CloseEvent) {
	if a.quitting {
		return
	}
	e.PreventDefault()
	a.win.Hide()
}
