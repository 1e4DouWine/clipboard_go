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
	typeMicro   = 0.75 // 类型徽章、快捷键帮助
)

// chromeRadiusOffset 是列表行/卡片圆角增量：在控件圆角基础上 +2，略软于控件。
const chromeRadiusOffset = 2

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

// header 渲染紧凑标题栏：图标 + 标题 + 条目计数 + 设置入口（单行）。
func (p *Presenter) header(c *ui.Context, t *ui.Theme) {
	ui.Row(c.Key("header")).Padding(t.Space(3), t.Space(4), t.Space(2)).
		Gap(t.Space(2.5)).AlignItems(ui.Center).Children(func() {
		// 图标：Accent 线稿，不再包厚重徽章底。
		ui.Icon(c, clipboardIcon).FontSize(t.Rem(1.25)).TextColor(t.Accent)
		ui.Text(c, p.t("Clipboard History")).FontSize(t.Rem(typeTitle)).Bold().Grow(1)
		// 条目计数：安静的 Caption 文本，不再是描边胶囊。
		ui.Text(c, p.tpl("%d items", len(p.items()))).
			FontSize(t.Rem(typeCaption)).TextColor(t.TextMuted)
		// 设置入口：右上角齿轮，打开设置 Modal（语言/主题/开机自启）。
		p.settingsButton(c, t)
	})
}

// settingsButton 渲染标题栏右侧齿轮按钮。
func (p *Presenter) settingsButton(c *ui.Context, t *ui.Theme) {
	btn := ui.ButtonBase(c.Key("settings-btn")).
		Label(p.t("Settings")).Tooltip(p.t("Settings"))
	btn.Padding(t.Space(1.25), t.Space(1.5)).Radius(t.Radius)
	if btn.Hovered() {
		btn.Background(t.SurfaceHover)
	}
	if btn.Pressed() {
		btn.Background(t.SurfacePressed)
	}
	btn.OnClick(func() {
		*p.Config.SettingsOpen = true
	}).Children(func() {
		ui.Icon(c, settingsIcon).FontSize(t.Rem(1.05)).TextColor(t.TextMuted)
	})
}

// toolbar 渲染搜索栏与分类过滤器。
func (p *Presenter) toolbar(c *ui.Context, t *ui.Theme) {
	ui.Column(c.Key("toolbar")).Padding(t.Space(1), t.Space(4), t.Space(2)).
		Gap(t.Space(1.5)).Children(func() {
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

// emptyState 渲染空状态：区分"没有历史"与"过滤无结果"两种场景。
func (p *Presenter) emptyState(c *ui.Context, t *ui.Theme, loading bool) {
	if len(p.items()) > 0 {
		return
	}
	ui.Column(c).Fill().Center().Padding(t.Space(8)).Gap(t.Space(1.5)).Children(func() {
		if loading {
			ui.Text(c, p.t("Loading history...")).FontSize(t.Rem(typeBody)).TextColor(t.TextMuted)
		} else if p.hasActiveFilter() {
			ui.Icon(c, searchIcon).FontSize(t.Rem(2.5)).TextColor(t.TextMuted.Alpha(0.35))
			// 过滤无结果：提示放宽条件。
			ui.Text(c, p.t("No matches")).FontSize(t.Rem(typeBody)).TextColor(t.Text)
			ui.Text(c, p.t("Try another keyword or category")).
				FontSize(t.Rem(typeCaption)).TextColor(t.TextMuted)
		} else {
			ui.Icon(c, searchIcon).FontSize(t.Rem(2.5)).TextColor(t.TextMuted.Alpha(0.35))
			// 没有历史：引导开始复制。
			ui.Text(c, p.t("Copy some text anywhere")).FontSize(t.Rem(typeBody)).TextColor(t.Text)
			ui.Text(c, p.t("It will appear here automatically")).
				FontSize(t.Rem(typeCaption)).TextColor(t.TextMuted)
		}
	})
}

// hasActiveFilter 报告当前是否有生效中的搜索/分类过滤（空状态文案分流用）。
//
// 关键词按空白判定：纯空白的查询在 store 层等价于"不过滤"，
// 此时不该显示"无匹配结果"，而应显示"还没有历史"。
func (p *Presenter) hasActiveFilter() bool {
	return strings.TrimSpace(*p.Config.Query) != "" || *p.Config.Filter != FilterAll
}

// statusBar 渲染底部状态栏：状态提示 / 快捷键帮助。
// 顶部一条极细发丝线，把工具栏/列表与页脚轻轻分开（Fluent 页脚习惯）。
// 开机自启与主题已迁入设置 Modal。
func (p *Presenter) statusBar(c *ui.Context, t *ui.Theme) {
	ui.Row(c.Key("statusbar")).Padding(t.Space(2), t.Space(4), t.Space(2.5)).
		Gap(t.Space(2)).AlignItems(ui.Center).
		BorderWidth(1, 0, 0, 0).BorderColor(t.Border.Alpha(0.6)).Children(func() {
		ui.Column(c).Grow(1).Children(func() {
			state := p.state()
			if state.Loading && len(state.Items) > 0 {
				ui.Text(c, p.t("Loading history...")).
					FontSize(t.Rem(typeCaption)).TextColor(t.TextMuted).SingleLine()
			} else if status := state.Status; status != "" {
				el := ui.Text(c, status).
					FontSize(t.Rem(typeCaption)).TextColor(t.TextMuted).SingleLine()
				// 提示出现时平滑淡入（曲线动画，文字变化不抖动）。
				el.Opacity(animateStatusOpacity(el, true))
			} else {
				ui.Text(c, p.t("↑↓ Choose  ·  ↵ Paste  ·  Del Delete  ·  Ctrl+D Favorite  ·  Esc Close  ·  Ctrl+Q Quit")).
					FontSize(t.Rem(typeMicro)).TextColor(t.TextMuted.Alpha(0.8)).SingleLine()
			}
		})
	})
}
