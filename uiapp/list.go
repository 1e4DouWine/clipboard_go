package uiapp

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"my-go-app/store"
)

// listSection 渲染条目列表（含空状态）。
//
// 不设置 ListState.Selected：mygo 会给选中行套一层 Accent 满底
// （Selected() 默认 Background(t.Accent)），与 Fluent 描边选中冲突。
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
		p.row(c, i, items, sel, !state.Loading)
	}).Fill().Children(func() {
		p.emptyState(c, t, state.Loading)
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
func (p *Presenter) row(c *ui.Context, i int, items []store.Entry, sel int, interactive bool) {
	e := items[i]
	t := c.Theme()

	ui.Box(c.Key(fmt.Sprintf("rowwrap-%d", e.ID))).Padding(t.Space(0.5), t.Space(3), 0).
		Children(func() {
			card := ui.Row(c.Key(fmt.Sprintf("row-%d", e.ID))).
				Padding(t.Space(2), t.Space(2.5)).Gap(t.Space(2)).
				AlignItems(ui.Center).
				Radius(t.Radius + chromeRadiusOffset).
				Transition(rowEnterExit) // 进出场动画：滑入/折叠

			hovered, pressed := card.Hovered(), card.Pressed()
			chosen := i == sel

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
			if interactive {
				card.OnClick(func() {
					p.activate(i) // 点击条目 = 立即粘贴（统一派发：回调缺失时安全忽略）
				})
			}
			card.Children(func() {
				p.rowChip(c, t, e)
				p.rowBody(c, t, e)
				p.rowStar(c, t, e)
			})
		})
}

// rowChip 渲染类型徽章（半透明语义色胶囊，Micro 字号，不抢预览）。
func (p *Presenter) rowChip(c *ui.Context, t *ui.Theme, e store.Entry) {
	fg, bg := chipColors(t, e.Kind)
	ui.Row(c.Key(fmt.Sprintf("chip-%d", e.ID))).
		Padding(t.Space(0.5), t.Space(1.5)).Radius(999).Background(bg).
		Children(func() {
			ui.Text(c, kindBadge(e.Kind)).FontSize(t.Rem(typeMicro)).TextColor(fg)
		})
}

// rowBody 渲染主体：预览文本 + 来源/时间元信息。
func (p *Presenter) rowBody(c *ui.Context, t *ui.Theme, e store.Entry) {
	ui.Column(c).Grow(1).Gap(t.Space(0.25)).Children(func() {
		ui.Text(c, e.Preview).FontSize(t.Rem(typeBody)).TextColor(t.Text).SingleLine()
		ui.Text(c, metaLine(e)).FontSize(t.Rem(typeCaption)).TextColor(t.TextMuted).SingleLine()
	})
}

// rowStar 渲染收藏星标：已收藏为警示色实心星（Bouncy 弹跳"点亮"），
// 未收藏为弱化空心星。
func (p *Presenter) rowStar(c *ui.Context, t *ui.Theme, e store.Entry) {
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
