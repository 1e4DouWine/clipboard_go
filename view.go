package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"my-go-app/store"
)

// 本文件是视图层：纯渲染 + 手势编排，不含业务逻辑（业务在 app.go）。
//
// 设计规范（Fluent / Win11）：
//   - 尺寸从 ui.Theme 派生：t.Space(n) 间距、t.Rem(n) 字号、t.Radius 圆角；
//     禁止硬编码像素值——保证明暗主题、缩放下的一致性；
//   - 语义色取自主题（t.Accent/t.Warning/t.Success/t.TextMuted），自动适配暗黑；
//   - 层次靠背景差与极细描边，不堆常驻卡片边框；
//   - 交互三态：悬停 t.SurfaceHover、按压 t.SurfacePressed、
//     选中 = 整圈 t.Accent 描边 + 轻投影（不铺填充）。
//
// 字号只允许四档（见 type* 常量）：Title / Body / Caption / Micro。

// 字号刻度（相对 t.FontSize 的 rem 系数）。新增文案必须落在这四档之一。
const (
	typeTitle   = 1.15 // 窗口标题
	typeBody    = 1.0  // 条目预览
	typeCaption = 0.85 // 元信息、状态栏、计数
	typeMicro   = 0.75 // 类型徽章、快捷键帮助
)

// chromeRadiusOffset 是列表行/卡片圆角增量：在控件圆角基础上 +2，略软于控件。
const chromeRadiusOffset = 2

// clipboardIcon 是标题栏应用图标（feather 风格剪贴板，线性描边、跟随文字色）。
var clipboardIcon = ui.MustParseSVG([]byte(
	`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M16 4h2a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2h2"/><rect x="8" y="2" width="8" height="4" rx="1" ry="1"/></svg>`))

// settingsIcon 是标题栏设置入口（feather 风格齿轮）。
var settingsIcon = ui.MustParseSVG([]byte(
	`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1 0 2.83 2 2 0 0 1-2.83 0l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-2 2 2 2 0 0 1-2-2v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83 0 2 2 0 0 1 0-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1-2-2 2 2 0 0 1 2-2h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 0-2.83 2 2 0 0 1 2.83 0l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 2-2 2 2 0 0 1 2 2v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 0 2 2 0 0 1 0 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 2 2 2 2 0 0 1-2 2h-.09a1.65 1.65 0 0 0-1.51 1z"/></svg>`))

// closeIcon 是设置页关闭按钮（feather 风格 X）。
var closeIcon = ui.MustParseSVG([]byte(
	`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M18 6L6 18"/><path d="M6 6l12 12"/></svg>`))

// searchIcon 是空状态装饰图标（放大镜）。
var searchIcon = ui.MustParseSVG([]byte(
	`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="11" cy="11" r="8"/><path d="m21 21-4.3-4.3"/></svg>`))

// applyFluentTheme 在帧开始处把当前主题复制为 Fluent 度量（控件圆角 4，
// 贴近 Win11），并应用到本帧上下文。明暗色板仍由 LightTheme/DarkTheme 决定。
func applyFluentTheme(c *ui.Context) *ui.Theme {
	base := c.Theme()
	fluent := *base
	fluent.Radius = 4
	c.SetTheme(&fluent)
	return &fluent
}

// view 在主线程构建界面：读取状态渲染，动作通过回调/notice 执行。
func (a *app) view(c *ui.Context) {
	t := applyFluentTheme(c)

	// 帧首快照：Modal 自带 Esc 关闭会在构建阶段把 settingsOpen 置 false，
	// 窗口级 Esc 需据此避免"关设置时连带隐藏主窗口"。
	settingsWasOpen := a.settingsOpen

	// 窗口级快捷键（与搜索框焦点无关）：↑↓ 选择、Enter 粘贴、Del 删除、Ctrl+D 收藏。
	if c.Shortcut(0, ui.KeyDown) {
		a.moveChosen(1)
	}
	if c.Shortcut(0, ui.KeyUp) {
		a.moveChosen(-1)
	}
	if c.Shortcut(0, ui.KeyEnter) {
		a.activate(a.chosen)
	}
	if c.Shortcut(0, ui.KeyDelete) {
		a.deleteChosen()
	}
	if c.Shortcut(ui.Cmd, ui.KeyD) {
		a.toggleFavoriteChosen()
	}
	// Ctrl+F 聚焦搜索框；Esc 优先关设置，其次隐藏窗口（搜索框内先由其自身清空内容）。
	c.OnShortcut(ui.Cmd, ui.KeyF, func() { a.input.Focus() })
	c.OnShortcut(0, ui.KeyEscape, func() {
		if a.settingsOpen {
			a.settingsOpen = false
			return
		}
		if settingsWasOpen {
			return // 本帧 Modal 已用 Esc 关闭设置
		}
		if a.win != nil {
			a.win.Hide()
		}
	})

	ui.Column(c).Fill().Gap(0).Children(func() {
		a.header(c, t)
		a.toolbar(c, t)
		a.listSection(c, t)
		a.statusBar(c, t)
	})
	a.settingsModal(c, t)
}

// header 渲染紧凑标题栏：图标 + 标题 + 条目计数 + 设置入口（单行）。
func (a *app) header(c *ui.Context, t *ui.Theme) {
	ui.Row(c.Key("header")).Padding(t.Space(3), t.Space(4), t.Space(2)).
		Gap(t.Space(2.5)).AlignItems(ui.Center).Children(func() {
		// 图标：Accent 线稿，不再包厚重徽章底。
		ui.Icon(c, clipboardIcon).FontSize(t.Rem(1.25)).TextColor(t.Accent)
		ui.Text(c, T("Clipboard History")).FontSize(t.Rem(typeTitle)).Bold().Grow(1)
		// 条目计数：安静的 Caption 文本，不再是描边胶囊。
		ui.Text(c, Tpl("%d items", len(a.items))).
			FontSize(t.Rem(typeCaption)).TextColor(t.TextMuted)
		// 设置入口：右上角齿轮，打开设置 Modal（语言/主题/开机自启）。
		a.settingsButton(c, t)
	})
}

// settingsButton 渲染标题栏右侧齿轮按钮。
func (a *app) settingsButton(c *ui.Context, t *ui.Theme) {
	btn := ui.ButtonBase(c.Key("settings-btn")).
		Label(T("Settings")).Tooltip(T("Settings"))
	btn.Padding(t.Space(1.25), t.Space(1.5)).Radius(t.Radius)
	if btn.Hovered() {
		btn.Background(t.SurfaceHover)
	}
	if btn.Pressed() {
		btn.Background(t.SurfacePressed)
	}
	btn.OnClick(func() {
		a.settingsOpen = true
	}).Children(func() {
		ui.Icon(c, settingsIcon).FontSize(t.Rem(1.05)).TextColor(t.TextMuted)
	})
}

// settingsModal 渲染设置对话框（ui.Modal：遮罩/Esc/关闭钮均可关）。
func (a *app) settingsModal(c *ui.Context, t *ui.Theme) {
	ui.Modal(c.Key("settings-modal"), &a.settingsOpen, func() {
		ui.Column(c).Gap(t.Space(3)).Children(func() {
			// 标题行
			ui.Row(c.Key("settings-title")).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
				ui.Text(c, T("Settings")).FontSize(t.Rem(typeTitle)).Bold().Grow(1)
				closeBtn := ui.ButtonBase(c.Key("settings-close")).
					Label(T("Close")).Tooltip(T("Close"))
				closeBtn.Padding(t.Space(1.25)).Radius(t.Radius)
				if closeBtn.Hovered() {
					closeBtn.Background(t.SurfaceHover)
				}
				closeBtn.OnClick(func() {
					a.settingsOpen = false
				}).Children(func() {
					ui.Icon(c, closeIcon).FontSize(t.Rem(1)).TextColor(t.TextMuted)
				})
			})

			// 语言
			ui.Column(c.Key("settings-lang")).Gap(t.Space(1.5)).Children(func() {
				ui.Text(c, T("Language")).FontSize(t.Rem(typeCaption)).TextColor(t.TextMuted)
				if ui.Segmented(c.Key("lang"), &a.langIdx, "English", "中文").
					Label(T("Language")).Changed() {
					a.setLang(a.langIdx)
				}
			})

			// 主题（自标题栏迁入）
			ui.Column(c.Key("settings-theme")).Gap(t.Space(1.5)).Children(func() {
				ui.Text(c, T("Theme")).FontSize(t.Rem(typeCaption)).TextColor(t.TextMuted)
				if ui.Segmented(c.Key("theme"), &a.themeIdx, themeModeLabels()...).
					Label(T("Theme")).Changed() {
					a.setTheme(a.themeIdx)
				}
			})

			// 开机自启（自状态栏迁入）
			if ui.Checkbox(c.Key("autostart"), &a.autoStart, T("Launch at login")).
				Label(T("Launch at login")).Changed() {
				a.toggleAutoStart(a.autoStart)
			}
		})
	})
}

// toolbar 渲染搜索栏与分类过滤器。
func (a *app) toolbar(c *ui.Context, t *ui.Theme) {
	ui.Column(c.Key("toolbar")).Padding(t.Space(1), t.Space(4), t.Space(2)).
		Gap(t.Space(1.5)).Children(func() {
		// 搜索框：占满整行，输入即时过滤。
		search := ui.SearchField(c.Key("search"), &a.query).
			Bind(&a.input).Label(T("Search history")).Grow(1)
		search.Placeholder(T("Search history"))
		if search.Changed() {
			a.reload()
		}
		// 分类过滤器：全部/文本/链接/代码/图片/收藏。
		if ui.Segmented(c.Key("filter"), &a.filter, filterLabelList()...).
			Label(T("Filter")).Changed() {
			a.reload()
		}
	})
}

// listSection 渲染条目列表（含空状态）。
//
// 不设置 ListState.Selected：mygo 会给选中行套一层 Accent 满底
// （Selected() 默认 Background(t.Accent)），与 Fluent 描边选中冲突。
// 选中索引仍由 a.chosen 驱动键盘/点击，视觉完全由 row() 绘制。
func (a *app) listSection(c *ui.Context, t *ui.Theme) {
	a.list.Key = func(i int) any { return a.items[i].ID } // 稳定行标识，刷新不丢滚动位置
	a.list.Label = func(i int) string { return a.items[i].Preview }
	ui.List(c.Key("history"), &a.list, len(a.items), func(i int) {
		a.row(c, i)
	}).Fill().Children(func() {
		a.emptyState(c, t)
	})
}

// emptyState 渲染空状态：区分"没有历史"与"过滤无结果"两种场景。
func (a *app) emptyState(c *ui.Context, t *ui.Theme) {
	if len(a.items) > 0 {
		return
	}
	ui.Column(c).Fill().Center().Padding(t.Space(8)).Gap(t.Space(1.5)).Children(func() {
		ui.Icon(c, searchIcon).FontSize(t.Rem(2.5)).TextColor(t.TextMuted.Alpha(0.35))
		if a.hasActiveFilter() {
			// 过滤无结果：提示放宽条件。
			ui.Text(c, T("No matches")).FontSize(t.Rem(typeBody)).TextColor(t.Text)
			ui.Text(c, T("Try another keyword or category")).
				FontSize(t.Rem(typeCaption)).TextColor(t.TextMuted)
		} else {
			// 没有历史：引导开始复制。
			ui.Text(c, T("Copy some text anywhere")).FontSize(t.Rem(typeBody)).TextColor(t.Text)
			ui.Text(c, T("It will appear here automatically")).
				FontSize(t.Rem(typeCaption)).TextColor(t.TextMuted)
		}
	})
}

// hasActiveFilter 报告当前是否有生效中的搜索/分类过滤（空状态文案分流用）。
func (a *app) hasActiveFilter() bool {
	return strings.TrimSpace(a.query) != "" || a.filter != filterAll
}

// statusBar 渲染底部状态栏：状态提示 / 快捷键帮助。
// 顶部一条极细发丝线，把工具栏/列表与页脚轻轻分开（Fluent 页脚习惯）。
// 开机自启与主题已迁入设置 Modal。
func (a *app) statusBar(c *ui.Context, t *ui.Theme) {
	ui.Row(c.Key("statusbar")).Padding(t.Space(2), t.Space(4), t.Space(2.5)).
		Gap(t.Space(2)).AlignItems(ui.Center).
		BorderWidth(1, 0, 0, 0).BorderColor(t.Border.Alpha(0.6)).Children(func() {
		ui.Column(c).Grow(1).Children(func() {
			if a.status != "" {
				el := ui.Text(c, a.status).
					FontSize(t.Rem(typeCaption)).TextColor(t.TextMuted).SingleLine()
				// 提示出现时平滑淡入（曲线动画，文字变化不抖动）。
				el.Opacity(animateStatusOpacity(el, true))
			} else {
				ui.Text(c, T("↑↓ Choose  ·  ↵ Paste  ·  Del Delete  ·  Ctrl+D Favorite  ·  Esc Close")).
					FontSize(t.Rem(typeMicro)).TextColor(t.TextMuted.Alpha(0.8)).SingleLine()
			}
		})
	})
}

// row 渲染单条历史：类型徽章 + 预览 + 元信息 + 收藏星，点击执行粘贴。
//
// Fluent 列表项：
//   - 默认无常驻边框，靠间距分隔；
//   - 悬停/按压用背景色表达；
//   - 选中 = 整圈 Accent 描边（不铺填充，文字始终清晰）+ 轻投影。
//
// 交互三态以动画过渡：
//   - 背景色 AnimateColor 平滑换色；
//   - 悬停上浮/按压缩小用 AnimatePose（只绘不建，性能最优）；
//   - 进出场用 rowEnterExit（新条目滑入、删除行折叠）。
func (a *app) row(c *ui.Context, i int) {
	e := a.items[i]
	t := c.Theme()

	ui.Box(c.Key(fmt.Sprintf("rowwrap-%d", e.ID))).Padding(t.Space(0.5), t.Space(3), 0).
		Children(func() {
			card := ui.Row(c.Key(fmt.Sprintf("row-%d", e.ID))).
				Padding(t.Space(2), t.Space(2.5)).Gap(t.Space(2)).
				AlignItems(ui.Center).
				Radius(t.Radius + chromeRadiusOffset).
				Transition(rowEnterExit) // 进出场动画：滑入/折叠

			hovered, pressed := card.Hovered(), card.Pressed()
			chosen := i == a.chosen

			// 背景色平滑过渡（按压 > 悬停 > 默认透明）。
			card.Background(animateRowBackground(card, hovered, pressed, t))
			// 选中：整圈 Accent 描边 + 极轻投影（不铺填充，预览始终可读）。
			if chosen {
				card.Border(1, t.Accent)
				card.Shadow(0, 1, 3, 0, t.Focus.Alpha(0.35))
			}
			// 悬停上浮/按压缩小（只绘不建）。
			card.AnimatePose(rowPose(hovered, pressed), ui.Snappy)

			// OnClick 在构造与输入处理后执行（mygo 约定），可安全改集合。
			card.OnClick(func() {
				a.chosen = i
				a.activate(i) // 点击条目 = 立即粘贴
			}).Children(func() {
				a.rowChip(c, t, e)
				a.rowBody(c, t, e)
				a.rowStar(c, t, e)
			})
		})
}

// rowChip 渲染类型徽章（半透明语义色胶囊，Micro 字号，不抢预览）。
func (a *app) rowChip(c *ui.Context, t *ui.Theme, e store.Entry) {
	fg, bg := chipColors(t, e.Kind)
	ui.Row(c.Key(fmt.Sprintf("chip-%d", e.ID))).
		Padding(t.Space(0.5), t.Space(1.5)).Radius(999).Background(bg).
		Children(func() {
			ui.Text(c, kindBadge(e.Kind)).FontSize(t.Rem(typeMicro)).TextColor(fg)
		})
}

// rowBody 渲染主体：预览文本 + 来源/时间元信息。
func (a *app) rowBody(c *ui.Context, t *ui.Theme, e store.Entry) {
	ui.Column(c).Grow(1).Gap(t.Space(0.25)).Children(func() {
		ui.Text(c, e.Preview).FontSize(t.Rem(typeBody)).TextColor(t.Text).SingleLine()
		ui.Text(c, metaLine(e)).FontSize(t.Rem(typeCaption)).TextColor(t.TextMuted).SingleLine()
	})
}

// rowStar 渲染收藏星标：已收藏为警示色实心星（Bouncy 弹跳"点亮"），
// 未收藏为弱化空心星。
func (a *app) rowStar(c *ui.Context, t *ui.Theme, e store.Entry) {
	star, color := "☆", t.TextMuted.Alpha(0.35)
	anim := ui.Smooth
	if e.IsFavorite {
		star, color = "★", t.Warning
		anim = ui.Bouncy // 收藏瞬间的弹跳强调
	}
	el := ui.Text(c, star).FontSize(t.Rem(typeBody)).TextColor(color)
	// 弹跳缩放（只绘不建）；键名随行 ID 稳定，收藏切换时弹簧自然过渡。
	el.AnimatePose(starPose(e.IsFavorite), anim)
}

// chipColors 返回类型徽章的前景/背景色。
//
// 配色逻辑：每种类型绑定一个主题语义色，背景取同色 10% 透明度，
// 明暗两套主题下均保持可读且不刺眼；未知类型回退为中性灰。
func chipColors(t *ui.Theme, k store.Kind) (fg, bg ui.Color) {
	switch k {
	case store.KindLink:
		return t.Accent, t.Accent.Alpha(0.10)
	case store.KindCode:
		return t.Warning, t.Warning.Alpha(0.10)
	case store.KindImage:
		return t.Success, t.Success.Alpha(0.10)
	default:
		return t.TextMuted, t.TextMuted.Alpha(0.10)
	}
}

// kindBadge 返回类型徽章文案。
func kindBadge(k store.Kind) string {
	switch k {
	case store.KindLink:
		return "URL"
	case store.KindCode:
		return "CODE"
	case store.KindImage:
		return "IMG"
	default:
		return "TXT"
	}
}

// metaLine 生成元信息行：来源应用 + 复制时间。
func metaLine(e store.Entry) string {
	when := time.UnixMilli(e.CopiedAt).Format("01-02 15:04")
	src := strings.TrimSpace(e.SourceApp)
	if src == "" {
		return when
	}
	// 窗口标题可能很长，截断避免挤压预览。
	if len([]rune(src)) > 40 {
		src = string([]rune(src)[:40]) + "…"
	}
	return src + " · " + when
}
