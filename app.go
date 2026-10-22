package main

import (
	"log"
	"strings"

	"github.com/egoist/mygo/ui"

	"my-go-app/applog"
	"my-go-app/fastpaste"
	"my-go-app/store"
	"my-go-app/theme"
)

// maxVisible 是列表一次加载的最大条数（内存过滤在此范围内进行）。
const maxVisible = 500

// filter* 是分类过滤器索引（与 ui.Segmented 选项一一对应；标签见 filterLabelList）。
const (
	filterAll = iota
	filterText
	filterLink
	filterCode
	filterImage
	filterFavorites
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

// fastPaster 是 paster 的生产实现，桥接 fastpaste 包函数。
type fastPaster struct{}

func (fastPaster) Capture() fastpaste.Target      { return fastpaste.Capture() }
func (fastPaster) Paste(t fastpaste.Target) error { return fastpaste.Paste(t) }

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
	theming *theme.Manager // 主题管理（可为 nil：测试环境不启用主题切换）
	login   loginOps       // 开机自启（可为 nil：测试环境不启用）

	// ── UI 状态（主线程）──
	items        []store.Entry    // 当前展示的条目（搜索 + 分类过滤后）
	query        string           // 搜索框绑定值
	filter       int              // 分类过滤索引（filterAll..filterFavorites）
	chosen       int              // 选中条目索引
	themeIdx     int              // 主题切换器绑定值（对应 theme.Mode.Index()）
	langIdx      int              // 语言切换器绑定值（对应 Lang.Index()）
	settingsOpen bool             // 设置 Modal 是否打开
	autoStart    bool             // 开机自启开关绑定值（启动时从系统状态同步）
	status       string           // 状态栏提示（错误降级、操作反馈）
	prevTarget   fastpaste.Target // 快速粘贴目标窗口（唤起前捕获）

	list  ui.ListState // 列表滚动/焦点状态（widget 状态，与业务数据分离）
	input ui.Handle    // 搜索框句柄（Ctrl+F 聚焦用）
}

// ---- 数据加载与过滤 ----

// reload 从 store 重新加载列表（搜索 + 分类过滤），并收敛选中索引。
//
// 内存优化：列表走 Brief 查询（不加载全文），避免大批量全文常驻内存；
// 全文在粘贴回写时按需 Get(id) 读取。
// 入库失败等错误只影响本次展示（status 提示），不阻塞界面。
func (a *app) reload() {
	if a.st == nil {
		return // 测试中可能没有 store
	}
	// 空查询走全量，非空走搜索（store 层已做 LIKE 转义）。
	var entries []store.Entry
	var err error
	if strings.TrimSpace(a.query) == "" {
		entries, err = a.st.ListBrief(maxVisible)
	} else {
		entries, err = a.st.SearchBrief(a.query, maxVisible)
	}
	if err != nil {
		log.Printf("加载历史失败: %v", err)
		a.status = T("Failed to load history. Check the log.")
		entries = nil
	}
	a.items = a.applyFilter(entries)
	a.clampChosen()
}

// applyFilter 按当前分类过滤条目（纯函数式，便于测试）。
func (a *app) applyFilter(entries []store.Entry) []store.Entry {
	switch a.filter {
	case filterAll:
		return entries
	case filterFavorites:
		out := make([]store.Entry, 0, len(entries))
		for _, e := range entries {
			if e.IsFavorite {
				out = append(out, e)
			}
		}
		return out
	default:
		// 文本/链接/代码/图片：按 kind 精确匹配。
		want := map[int]store.Kind{
			filterText:  store.KindText,
			filterLink:  store.KindLink,
			filterCode:  store.KindCode,
			filterImage: store.KindImage,
		}[a.filter]
		out := make([]store.Entry, 0, len(entries))
		for _, e := range entries {
			if e.Kind == want {
				out = append(out, e)
			}
		}
		return out
	}
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
func (a *app) moveChosen(delta int) {
	a.chosen += delta
	a.clampChosen()
	if len(a.items) > 0 {
		a.list.ScrollIntoView(a.chosen) // 让选中项保持可见
	}
}

// ---- 条目操作 ----

// activate 选中条目"粘贴"：写回剪贴板 → 隐藏窗口 → 快速粘贴到原窗口。
//
// 降级语义：写回是保底动作，必须先成功；快速粘贴失败（目标窗口关闭、
// 被系统拒绝等）只提示用户手动粘贴，不影响已完成的写回。
func (a *app) activate(i int) {
	if i < 0 || i >= len(a.items) {
		return // 边界：列表为空或索引越界
	}
	e := a.items[i]

	if err := a.writeBack(e); err != nil {
		log.Printf("写回剪贴板失败: %v", err)
		a.status = Tpl("Failed to write clipboard: %s", err.Error())
		return
	}

	// 先隐藏再注入：让出前台焦点，按键才能落到目标窗口。
	if a.win != nil {
		a.win.Hide()
	}
	if a.paste == nil {
		return // 未配置快速粘贴（测试环境）
	}
	if err := a.paste.Paste(a.prevTarget); err != nil {
		// 注入失败是可预期故障（见 fastpaste 文档）：提示手动粘贴。
		a.status = T("Copied to clipboard (auto-paste failed, paste manually)")
		log.Printf("快速粘贴失败: %v", err)
	}
}

// writeBack 把条目写回系统剪贴板：文本走 WriteText，图片走 ReadImage+WriteImage。
//
// 内存优化：列表条目不含全文（Brief 查询），文本回写时按需 Get(id) 取全文，
// 写完即释放引用——大文本不会长期驻留在 UI 状态里。
func (a *app) writeBack(e store.Entry) error {
	if a.clip == nil {
		return nil
	}
	if e.Kind == store.KindImage {
		data, err := a.st.ReadImage(e)
		if err != nil {
			return err // 图片文件丢失等，提示用户删除该条目
		}
		return a.clip.WriteImage(data)
	}
	// 文本条目：FullText 为空说明来自 Brief 查询，按需取全文。
	text := e.FullText
	if text == "" && a.st != nil {
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
	if a.chosen < 0 || a.chosen >= len(a.items) {
		return
	}
	id := a.items[a.chosen].ID
	if err := a.st.Delete(id); err != nil {
		log.Printf("删除条目失败: %v", err)
		a.status = Tpl("Failed to delete: %s", err.Error())
		return
	}
	a.reload()
}

// toggleFavoriteChosen 切换选中条目的收藏标记。
func (a *app) toggleFavoriteChosen() {
	if a.chosen < 0 || a.chosen >= len(a.items) {
		return
	}
	if _, err := a.st.ToggleFavorite(a.items[a.chosen].ID); err != nil {
		log.Printf("切换收藏失败: %v", err)
		a.status = Tpl("Action failed: %s", err.Error())
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
	if a.login == nil {
		return // 测试环境未启用
	}
	if err := a.login.SetOpenAtLogin(on); err != nil {
		log.Printf("设置开机自启失败: %v", err)
		a.autoStart = a.login.OpenAtLogin() // 回滚为系统实际状态
		a.status = T("Failed to enable launch at login. Check system permissions.")
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
	if a.theming == nil {
		return // 测试环境未启用
	}
	mode := theme.ModeOfIndex(idx)
	if err := a.theming.SetMode(mode); err != nil {
		log.Printf("主题切换: %v", err)
		a.status = T("Appearance changed (save failed; will reset after restart)")
		return
	}
	a.status = ""
}

// setLang 切换界面语言（设置页切换器回调，主线程执行）。
//
// 语义：文案立即全局生效（T 读全局 lang）；持久化失败仅提示。
func (a *app) setLang(idx int) {
	l := LangOfIndex(idx)
	lang = l
	a.langIdx = l.Index()
	applyMygoLocale() // 同步控件 chrome（Search 占位符等）
	a.syncWindowTitle()
	if a.st == nil {
		return
	}
	if err := SaveLang(a.st, l); err != nil {
		log.Printf("保存语言偏好失败: %v", err)
		a.status = T("Language changed (save failed; will reset after restart)")
		return
	}
	a.status = ""
}

// syncWindowTitle 把系统窗口标题同步到当前语言。
// App.SetName 保持产品标识不变（改名会迁移数据目录），只动窗口标题。
func (a *app) syncWindowTitle() {
	if a.win == nil {
		return
	}
	a.win.SetTitle(T("Clipboard History"))
}

// ---- 窗口与全局快捷键 ----

// toggleWindow 唤起/隐藏主窗口（全局快捷键回调，主线程执行）。
//
// 快速粘贴的关键时序：唤起前（前台仍是用户的原窗口）先 Capture 目标，
// 粘贴时才能回填到正确窗口。
func (a *app) toggleWindow() {
	if a.win == nil {
		return
	}
	if a.win.IsVisible() {
		a.win.Hide()
		return
	}
	if a.paste != nil {
		a.prevTarget = a.paste.Capture() // 必须在 Show 之前捕获
	}
	a.reload() // 每次唤起刷新，反映最新历史
	a.status = ""
	a.win.Show()
	a.win.Focus()
}

// onCaptured 由 watch 在后台 goroutine 调用：只做一件事——把刷新投递主线程。
// （win.Update 是异步的，绝不能在这里直接改 UI 状态。）
func (a *app) onCaptured(e store.Entry) {
	if a.win == nil {
		return
	}
	a.win.Update(func() {
		// 主线程刷新兜底：panic 记录后不崩溃（列表刷新失败不影响已入库数据）。
		defer applog.Recover("onCaptured-refresh")
		a.reload()
	})
}

// ---- 视图 ----
//（渲染层已拆分到 view.go：header/toolbar/list/statusBar/row 等纯渲染函数。）

// safeView 是 view 的崩溃兜底（见 guardView）。
func (a *app) safeView(c *ui.Context) { guardView(a.view)(c) }

// guardView 包装任意视图函数：构建 panic 时记录堆栈并渲染降级界面，
// 而不是让整个应用崩溃。
//
// 设计说明：正常路径不应触发 recover（mygo 的过期元素等问题应当修复所有权），
// 这里的兜底针对的是无法预期的渲染缺陷——用户体验上"界面降级"远好于"应用闪退"。
// 降级渲染本身不再触碰任何业务状态，避免连锁崩溃。
func guardView(inner func(c *ui.Context)) func(c *ui.Context) {
	return func(c *ui.Context) {
		defer func() {
			if r := recover(); r != nil {
				applog.LogPanic("view", r)
				// 二次兜底：降级渲染自身若再 panic，仅记录不再渲染。
				defer func() {
					if r2 := recover(); r2 != nil {
						applog.LogPanic("view-degraded", r2)
					}
				}()
				ui.Column(c).Fill().Center().Padding(24).Gap(8).Children(func() {
					ui.Text(c, T("UI render error, showing fallback"))
					ui.Text(c, T("See the log file for details"))
				})
			}
		}()
		inner(c)
	}
}
