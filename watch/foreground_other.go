//go:build !windows

package watch

// foregroundTitle 在非 Windows 平台没有实现，返回空串。
//
// 影响：敏感窗口过滤与来源记录会被跳过（isSensitive("") 恒为 false），
// 其余捕获功能不受影响。后续如需支持 macOS/Linux，
// 可在此文件按平台接入 Accessibility API / xdotool 等能力。
func foregroundTitle() string {
	return ""
}
