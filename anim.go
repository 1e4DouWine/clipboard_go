package main

import (
	"time"

	"github.com/egoist/mygo/ui"
)

// 本文件是动效层：集中定义交互动画的参数与应用方式，view.go 在渲染时调用。
//
// 设计原则（性能优先）：
//   - 元素位移/缩放优先用 AnimatePose（"只绘不建"：动画帧只重绘不重建视图，
//     是最便宜的动效），避免整帧重建开销；
//   - 颜色过渡用 AnimateColor（会逐帧重建视图，仅用于小范围如单行卡片）；
//   - 进出场用 ElementTransition（由布局层驱动，自动处理 Enter/Exit）；
//   - 无障碍：系统开启"减少动态效果"时 mygo 动画 API 自动降级
//    （直接到位 + 保留淡入），无需业务分支，测试已覆盖该路径。
//
// 动画节奏（SwiftUI/Motion 风格弹簧参数）：
//   - 快速反馈（悬停/按压）：ui.Snappy（约 250ms 微回弹）；
//   - 平滑换色：ui.Smooth（约 300ms 无回弹）；
//   - 强调动作（收藏星弹跳）：ui.Bouncy（可见回弹，给"点亮"的愉悦感）。

// 动画时长常量（曲线类动画用）。
const (
	// statusFadeIn 是状态栏提示的淡入时长。
	statusFadeIn = 180 * time.Millisecond
)

// rowEnterExit 是条目卡片的进出场动画：
//   - Enter：从上方 8DIP 滑入 + 淡入（新捕获的条目"落位"感）；
//   - Exit：折叠 + 淡出（删除时行高收缩，后续条目平滑补位）。
//
// Duration 0 使用默认 200ms；Ease nil 使用默认 EaseOut。
var rowEnterExit = ui.ElementTransition{
	Enter: &ui.Motion{Y: -8, Opacity: 1},
	Exit:  &ui.Motion{Opacity: 0, Collapse: true},
}

// rowPose 返回卡片的悬停/按压姿态（AnimatePose 只绘不建，性能最优）。
//
// 三态：
//   - 按压：轻微缩小（0.985），"按下去"的触感；
//   - 悬停：上浮 1.5DIP + 微放大（1.005），"可点击"的暗示；
//   - 默认/选中：归位（选中的视觉由颜色与投影表达，不额外位移）。
func rowPose(hovered, pressed bool) ui.Pose {
	switch {
	case pressed:
		return ui.Pose{Scale: 0.985}
	case hovered:
		return ui.Pose{Y: -1.5, Scale: 1.005}
	default:
		return ui.Pose{}
	}
}

// starPose 返回收藏星的姿态：
//   - 已收藏：放大 1.25（配合 Bouncy 弹簧，"点亮"弹跳）；
//   - 未收藏：原尺寸。
func starPose(favorite bool) ui.Pose {
	if favorite {
		return ui.Pose{Scale: 1.25}
	}
	return ui.Pose{}
}

// animateRowBackground 计算卡片背景色的平滑过渡值。
//
// 目标色由交互状态决定（按压 > 悬停 > 默认透明），AnimateColor 以
// premultiplied alpha 混色，半透明过渡不发灰。默认透明让列表与画布同层。
//
// 选中不铺填充（避免整行糊成色块），交给边框描边表达；背景仅在
// 按压/悬停时出现 Fluent 软分层。
func animateRowBackground(card ui.Element, hovered, pressed bool, t *ui.Theme) ui.Color {
	// 目标色选择（与静态视图逻辑一致，这里只是加了过渡）。
	target := ui.Transparent
	switch {
	case pressed:
		target = t.SurfacePressed
	case hovered:
		target = t.SurfaceHover
	}
	// 键名含行内位置语义：同一卡片跨帧同键才有连续动画（c.Key 已保证稳定标识）。
	return card.AnimateColor("row-bg", target, ui.Smooth)
}

// animateStatusOpacity 计算状态栏提示的淡入值。
//
// 提示文字出现时从 0 淡入到 1（平滑无回弹），消失即止——
// 用曲线动画而非弹簧，避免文字内容变化时的抖动。
func animateStatusOpacity(el ui.Element, visible bool) float32 {
	target := float32(0)
	if visible {
		target = 1
	}
	return el.AnimateTo("status-fade", target, ui.Animation{
		Duration: statusFadeIn,
		Ease:     ui.EaseOut,
	})
}
