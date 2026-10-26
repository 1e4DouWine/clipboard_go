package uiapp

import "github.com/egoist/mygo/ui"

// rowPose 只在按压时轻微缩小，不改变布局或悬停位置。
func rowPose(pressed bool) ui.Pose {
	if pressed {
		return ui.Pose{Scale: 0.99}
	}
	return ui.Pose{}
}

// rowBackground 即时反映按压、选中和悬停状态。
func rowBackground(t *ui.Theme, hovered, pressed, chosen bool) ui.Color {
	switch {
	case pressed || chosen && hovered:
		return t.SurfacePressed
	case chosen:
		return t.SurfaceHover
	case hovered:
		return t.SurfaceHover.Alpha(0.6)
	default:
		return ui.Transparent
	}
}
