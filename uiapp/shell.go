package uiapp

import (
	"strings"

	"github.com/egoist/mygo/ui"

	"my-go-app/store"
)

// 分类过滤器索引（与 ui.Segmented 选项一一对应；标签见 i18n.Locale.FilterLabels）。
const (
	FilterAll = iota
	FilterText
	FilterLink
	FilterCode
	FilterImage
	FilterFavorites
)

// FilterKind 把分类索引映射为内容类型。
// "全部"与"收藏"不限类型，返回空串（表示不按类型过滤）。
func FilterKind(idx int) store.Kind {
	switch idx {
	case FilterText:
		return store.KindText
	case FilterLink:
		return store.KindLink
	case FilterCode:
		return store.KindCode
	case FilterImage:
		return store.KindImage
	default:
		return ""
	}
}

// 字号刻度（相对 t.FontSize 的 rem 系数）。新增文案必须落在这四档之一。
const (
	typeTitle   = 1.15 // 窗口标题
	typeBody    = 1.0  // 条目预览
	typeCaption = 0.85 // 元信息、状态栏、计数
	typeMicro   = 0.75 // 快捷键提示
)

// chromeRadiusOffset 是列表行/卡片圆角增量：在控件圆角基础上 +2，略软于控件。
const chromeRadiusOffset = 2

// 外壳尺寸（DIP）。
const (
	headerTileSize = 28 // 标题栏应用图标块
	iconButtonSize = 28 // 图标按钮（设置、关闭）
	emptyTileSize  = 56 // 空状态圆形图标底
	emptyIconSize  = 24 // 空状态图标
)

// applyFluentTheme 在帧开始处把当前主题复制为 Fluent 度量（控件圆角 4，
// 贴近 Win11），并应用到本帧上下文。明暗色板仍由 LightTheme/DarkTheme 决定。
func applyFluentTheme(c *ui.Context) *ui.Theme {
	base := c.Theme()
	fluent := *base
	fluent.Radius = 4
	c.SetTheme(&fluent)
	return &fluent
}

// View 在主线程构建界面：读取状态渲染，动作通过回调/notice 执行。
func (p *Presenter) View(c *ui.Context) {
	t := applyFluentTheme(c)

	// 帧首快照：Modal 自带 Esc 关闭会在构建阶段把设置开关置 false，
	// 窗口级 Esc 需据此避免"关设置时连带隐藏主窗口"。
	settingsWasOpen := *p.Config.SettingsOpen

	// 窗口级快捷键（与搜索框焦点无关）：↑↓ 选择、Enter 粘贴、Del 删除、Ctrl+D 收藏。
	if c.Shortcut(0, ui.KeyDown) {
		p.moveChosen(1)
	}
	if c.Shortcut(0, ui.KeyUp) {
		p.moveChosen(-1)
	}
	if c.Shortcut(0, ui.KeyEnter) {
		p.activate(p.chosen())
	}
	if c.Shortcut(0, ui.KeyDelete) {
		p.deleteChosen()
	}
	if c.Shortcut(ui.Cmd, ui.KeyD) {
		p.toggleFavorite()
	}
	// Ctrl+Q 退出应用（关窗只是隐藏，后台常驻，没有它就无法真正退出）。
	if c.Shortcut(ui.Cmd, ui.KeyQ) {
		p.quit()
	}
	// Ctrl+F 聚焦搜索框；Esc 优先关设置，其次隐藏窗口（搜索框内先由其自身清空内容）。
	c.OnShortcut(ui.Cmd, ui.KeyF, func() { p.search.Focus(c) })
	c.OnShortcut(0, ui.KeyEscape, func() {
		if *p.Config.SettingsOpen {
			*p.Config.SettingsOpen = false
			return
		}
		if settingsWasOpen {
			return // 本帧 Modal 已用 Esc 关闭设置
		}
		p.hideWindow()
	})

	ui.Column(c).Fill().Gap(0).Children(func() {
		p.header(c, t)
		p.toolbar(c, t)
		p.listSection(c, t)
		p.statusBar(c, t)
	})
	p.settingsModal(c, t)
}

// moveChosen 上下移动选中项，并把列表滚动到新位置。
//
// 选中索引由应用层维护（业务状态），滚动由视图负责（控件状态）：
// 命令发出后读回最新索引再滚动，避免两边各自维护一份选中位置。
func (p *Presenter) moveChosen(delta int) {
	p.Actions.MoveChosen(delta)
	idx := p.chosen()
	if idx >= 0 && idx < len(p.items()) {
		p.list.ScrollIntoView(idx)
	}
}

// activate 激活（粘贴）指定条目。
func (p *Presenter) activate(i int) {
	p.Actions.Activate(i)
}

// deleteChosen 删除选中条目。
func (p *Presenter) deleteChosen() {
	p.Actions.DeleteChosen()
}

// toggleFavorite 切换选中条目的收藏。
func (p *Presenter) toggleFavorite() {
	p.Actions.ToggleFavorite()
}

// hideWindow 隐藏主窗口。
func (p *Presenter) hideWindow() {
	p.Actions.HideWindow()
}

// quit 退出应用。
func (p *Presenter) quit() {
	p.Actions.Quit()
}

// header 渲染紧凑标题栏：应用图标块 + 标题 + 条目计数 + 设置入口（单行）。
func (p *Presenter) header(c *ui.Context, t *ui.Theme) {
	ui.Row(c.Key("header")).Padding(t.Space(3.5), t.Space(4), t.Space(2.5)).
		Gap(t.Space(2.5)).AlignItems(ui.Center).Children(func() {
		// 应用图标：Accent 浅底方块，与条目类型图标块同一视觉语言。
		ui.Box(c.Key("app-icon")).Size(headerTileSize, headerTileSize).Shrink(0).Center().
			Radius(t.Radius + chromeRadiusOffset).Background(t.Accent.Alpha(0.12)).
			Children(func() {
				ui.Icon(c, clipboardIcon).Size(kindIconSize, kindIconSize).TextColor(t.Accent)
			})
		ui.Text(c, p.t("Clipboard History")).FontSize(t.Rem(typeTitle)).FontWeight(600).
			SingleLine().Grow(1)
		// 条目计数：安静的 Caption 文本。
		ui.Text(c, p.tpl("%d items", len(p.items()))).
			FontSize(t.Rem(typeCaption)).TextColor(t.TextMuted).SingleLine()
		// 设置入口：右上角齿轮，打开设置 Modal（语言/主题/开机自启）。
		iconButton(c, t, "settings-btn", settingsIcon, p.t("Settings"), func() {
			*p.Config.SettingsOpen = true
		})
	})
}

// iconButton 渲染无底色的图标按钮（悬停/按压出现 Fluent 软底）。
// label 同时作为提示与无障碍名称。
func iconButton(c *ui.Context, t *ui.Theme, key string, icon *ui.SVG, label string, onClick func()) {
	btn := ui.ButtonBase(c.Key(key)).Label(label).Tooltip(label)
	btn.Size(iconButtonSize, iconButtonSize).Center().Radius(t.Radius)
	bg := ui.Transparent
	switch {
	case btn.Pressed():
		bg = t.SurfacePressed
	case btn.Hovered():
		bg = t.SurfaceHover
	}
	btn.Background(bg)
	btn.OnClick(onClick).Children(func() {
		ui.Icon(c, icon).Size(kindIconSize, kindIconSize).TextColor(t.TextMuted)
	})
}

// toolbar 渲染搜索栏与分类过滤器。
func (p *Presenter) toolbar(c *ui.Context, t *ui.Theme) {
	ui.Column(c.Key("toolbar")).Padding(0, t.Space(4), t.Space(2)).
		Gap(t.Space(2)).Children(func() {
		// 搜索框：占满整行，输入即时过滤。
		search := ui.SearchField(c.Key("search"), p.Config.Query).
			Bind(&p.search).Label(p.t("Search history")).Grow(1)
		search.Placeholder(p.t("Search history"))
		if search.Changed() {
			p.searchChanged()
		}
		// 分类过滤器：全部/文本/链接/代码/图片/收藏。
		if ui.Segmented(c.Key("filter"), p.Config.Filter, p.loc().FilterLabels()...).
			Label(p.t("Filter")).Changed() {
			p.filterChanged()
		}
	})
}

// searchChanged 通知应用层搜索词已变（由应用层决定防抖与重载时机）。
func (p *Presenter) searchChanged() {
	p.Actions.SearchChanged()
}

// filterChanged 通知应用层分类过滤已变。
func (p *Presenter) filterChanged() {
	p.Actions.FilterChanged()
}

// emptyState 渲染空状态：区分"加载中""没有历史"与"过滤无结果"三种场景。
func (p *Presenter) emptyState(c *ui.Context, t *ui.Theme, loading bool) {
	if len(p.items()) > 0 {
		return
	}
	ui.Column(c).Fill().Center().Padding(t.Space(8)).Gap(t.Space(2)).Children(func() {
		if loading {
			ui.Text(c, p.t("Loading history...")).FontSize(t.Rem(typeBody)).TextColor(t.TextMuted)
			return
		}
		icon, title, hint := clipboardIcon, "Copy some text anywhere", "It will appear here automatically"
		if p.hasActiveFilter() {
			// 过滤无结果：提示放宽条件。
			icon, title, hint = searchIcon, "No matches", "Try another keyword or category"
		}
		// 圆形浅底图标，比裸图标更稳、更像系统空状态插画。
		ui.Box(c.Key("empty-icon")).Size(emptyTileSize, emptyTileSize).Center().
			Radius(emptyTileSize / 2).Background(t.SurfaceHover).MarginY(t.Space(1)).
			Children(func() {
				ui.Icon(c, icon).Size(emptyIconSize, emptyIconSize).TextColor(t.TextMuted)
			})
		ui.Text(c, p.t(title)).FontSize(t.Rem(typeBody)).FontWeight(600).TextColor(t.Text)
		ui.Text(c, p.t(hint)).FontSize(t.Rem(typeCaption)).TextColor(t.TextMuted)
	})
}

// hasActiveFilter 报告当前是否有生效中的搜索/分类过滤（空状态文案分流用）。
//
// 关键词按空白判定：纯空白的查询在 store 层等价于"不过滤"，
// 此时不该显示"无匹配结果"，而应显示"还没有历史"。
func (p *Presenter) hasActiveFilter() bool {
	return strings.TrimSpace(*p.Config.Query) != "" || *p.Config.Filter != FilterAll
}

// shortcutHints 是状态栏的快捷键提示（键帽文字 + 英文动作源串）。
// ↑↓ 选择属于列表常识，不占状态栏宽度。
var shortcutHints = []struct{ key, action string }{
	{"Enter", "Paste"},
	{"Ctrl+D", "Favorite"},
	{"Del", "Delete"},
	{"Esc", "Close"},
	{"Ctrl+Q", "Quit"},
}

// statusBar 渲染底部状态栏：状态提示优先，否则显示键帽式快捷键帮助。
// 顶部一条极细发丝线，把列表与页脚轻轻分开（Fluent 页脚习惯）。
func (p *Presenter) statusBar(c *ui.Context, t *ui.Theme) {
	ui.Row(c.Key("statusbar")).Padding(t.Space(2), t.Space(4)).
		Gap(t.Space(1)).AlignItems(ui.Center).ClipX().
		BorderWidth(1, 0, 0, 0).BorderColor(t.Border.Alpha(0.6)).Children(func() {
		state := p.state()
		if state.Loading && len(state.Items) > 0 {
			ui.Text(c, p.t("Loading history...")).
				FontSize(t.Rem(typeCaption)).TextColor(t.TextMuted).SingleLine()
			return
		}
		if status := state.Status; status != "" {
			ui.Text(c, status).
				FontSize(t.Rem(typeCaption)).TextColor(t.TextMuted).SingleLine()
			return
		}
		for _, h := range shortcutHints {
			ui.Row(c.Key("hint-" + h.action)).Gap(t.Space(1)).AlignItems(ui.Center).Shrink(0).
				Children(func() {
					keycap(c, t, h.key)
					ui.Text(c, p.t(h.action)).FontSize(t.Rem(typeMicro)).
						TextColor(t.TextMuted).SingleLine()
				})
		}
	})
}

// keycap 渲染一个键帽：细描边小方块 + Micro 字号键名。
func keycap(c *ui.Context, t *ui.Theme, key string) {
	ui.Box(c.Key("keycap-"+key)).Padding(0, t.Space(1.25)).MinWidth(t.Space(4.5)).Center().
		Radius(t.Radius-1).Border(1, t.Border).Background(t.Surface).
		Children(func() {
			ui.Text(c, key).FontSize(t.Rem(typeMicro)).TextColor(t.TextMuted).SingleLine()
		})
}
