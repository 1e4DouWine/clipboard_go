package uiapp

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"my-go-app/i18n"
	"my-go-app/store"
)

// 行内尺寸（DIP）。类型图标块固定尺寸，保证不同类型的预览文字左对齐。
const (
	kindTileSize   = 32 // 类型图标块边长
	kindIconSize   = 16 // 图标块内图标尺寸
	indicatorW     = 3  // 选中指示条宽度
	indicatorH     = 16 // 选中指示条高度
	maxSourceRunes = 40 // 来源窗口标题截断长度
)

// listSection 渲染条目列表（含空状态）。
//
// 不设置 ListState.Selected：mygo 会给选中行套一层 Accent 满底
// （Selected() 默认 Background(t.Accent)），与 Fluent 选中样式冲突。
// 选中索引仍由 State.Chosen 驱动键盘/点击，视觉完全由 row() 绘制。
func (p *Presenter) listSection(c *ui.Context, t *ui.Theme) {
	// 快照只取一次：本帧的条目与选中项对所有行共享，
	// 避免每行都回调一次 Snapshot（列表可达数百行）。
	state := p.state()
	items, sel := state.Items, state.Chosen
	// 稳定行标识，刷新不丢滚动位置。
	p.list.Key = func(i int) any { return items[i].ID }
	// Label 提供行的无障碍名称（读屏与首字母定位都依赖它）。
	p.list.Label = func(i int) string { return items[i].Preview }
	ui.List(c.Key("history"), &p.list, len(items), func(i int) {
		p.row(c, i, items[i], i == sel, !state.Loading, state.Locale)
	}).Fill().Padding(0, 0, t.Space(1)).Children(func() {
		p.emptyState(c, t, state.Loading)
	})
}

// row 渲染单条历史：类型图标块 + 预览/元信息 + 收藏星，点击执行粘贴。
//
// Fluent（Win11）列表项：
//   - 默认透明，靠间距分隔；
//   - 悬停/按压/选中用背景色分层表达；
//   - 选中额外在左侧绘制 Accent 竖条指示器（与系统列表一致）。
//
// 仅按压缩小使用 AnimatePose，列表增删保持即时反馈。
func (p *Presenter) row(c *ui.Context, i int, e store.Entry, chosen, interactive bool, loc i18n.Locale) {
	t := c.Theme()

	ui.Box(c.Key(fmt.Sprintf("rowwrap-%d", e.ID))).Padding(t.Space(0.5), t.Space(2), 0).
		Children(func() {
			card := ui.Row(c.Key(fmt.Sprintf("row-%d", e.ID))).
				Padding(t.Space(2), t.Space(3), t.Space(2), t.Space(2.5)).Gap(t.Space(3)).
				AlignItems(ui.Center).
				Radius(t.Radius + chromeRadiusOffset)

			hovered, pressed := card.Hovered(), card.Pressed()
			card.Background(rowBackground(t, hovered, pressed, chosen))
			card.AnimatePose(rowPose(pressed), ui.Snappy)
			if chosen {
				// 选中指示条：左缘垂直居中的 Accent 胶囊（只绘制，不参与布局）。
				accent := t.Accent
				card.DrawOver(func(pt *ui.Painter, r ui.Rect) {
					pt.Fill(ui.Rect{
						X: r.X,
						Y: r.Y + (r.H-indicatorH)/2,
						W: indicatorW,
						H: indicatorH,
					}, accent, indicatorW/2)
				})
			}

			// OnClick 在构造与输入处理后执行（mygo 约定），可安全改集合。
			if interactive {
				card.OnClick(func() {
					p.activate(i) // 点击条目 = 立即粘贴
				})
			}
			card.Children(func() {
				p.kindTile(c, t, e, loc)
				p.rowBody(c, t, e)
				p.rowStar(c, t, e, loc)
			})
		})
}

// kindTile 渲染固定尺寸的类型图标块（语义色半透明底 + 线性图标）。
func (p *Presenter) kindTile(c *ui.Context, t *ui.Theme, e store.Entry, loc i18n.Locale) {
	fg, bg := chipColors(t, e.Kind)
	ui.Box(c.Key(fmt.Sprintf("kind-%d", e.ID))).
		Size(kindTileSize, kindTileSize).Shrink(0).Center().
		Radius(t.Radius + chromeRadiusOffset).Background(bg).
		Tooltip(loc.T(kindLabel(e.Kind))).
		Children(func() {
			ui.Icon(c, kindIcon(e.Kind)).Size(kindIconSize, kindIconSize).TextColor(fg)
		})
}

// rowBody 渲染主体：预览文本 + 来源/时间元信息。
func (p *Presenter) rowBody(c *ui.Context, t *ui.Theme, e store.Entry) {
	ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Gap(t.Space(0.5)).Children(func() {
		ui.Text(c, e.Preview).FontSize(t.Rem(typeBody)).TextColor(t.Text).SingleLine()
		ui.Text(c, metaLine(e)).
			FontSize(t.Rem(typeCaption)).TextColor(t.TextMuted).SingleLine()
	})
}

// rowStar 仅为收藏条目显示实心星；宽度固定，切换收藏时预览不跳动。
func (p *Presenter) rowStar(c *ui.Context, t *ui.Theme, e store.Entry, loc i18n.Locale) {
	ui.Box(c.Key(fmt.Sprintf("star-%d", e.ID))).Size(kindIconSize, kindIconSize).Shrink(0).
		Children(func() {
			if !e.IsFavorite {
				return
			}
			ui.Icon(c, starIcon).Size(kindIconSize, kindIconSize).
				TextColor(t.Warning).Label(loc.T("Favorited"))
		})
}

// chipColors 返回类型图标块的前景/背景色。
//
// 配色逻辑：每种类型绑定一个主题语义色，背景取同色 12% 透明度，
// 明暗两套主题下均保持可读且不刺眼；未知类型回退为中性灰。
func chipColors(t *ui.Theme, k store.Kind) (fg, bg ui.Color) {
	switch k {
	case store.KindLink:
		return t.Accent, t.Accent.Alpha(0.12)
	case store.KindCode:
		return t.Warning, t.Warning.Alpha(0.12)
	case store.KindImage:
		return t.Success, t.Success.Alpha(0.12)
	default:
		return t.TextMuted, t.TextMuted.Alpha(0.12)
	}
}

// kindIcon 返回类型图标。
func kindIcon(k store.Kind) *ui.SVG {
	switch k {
	case store.KindLink:
		return linkIcon
	case store.KindCode:
		return codeIcon
	case store.KindImage:
		return imageIcon
	default:
		return textIcon
	}
}

// kindLabel 返回类型名称的英文源串（经 Locale.T 翻译，用于提示与读屏）。
func kindLabel(k store.Kind) string {
	switch k {
	case store.KindLink:
		return "Link"
	case store.KindCode:
		return "Code"
	case store.KindImage:
		return "Image"
	default:
		return "Text"
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
	if r := []rune(src); len(r) > maxSourceRunes {
		src = string(r[:maxSourceRunes]) + "…"
	}
	return src + " · " + when
}
