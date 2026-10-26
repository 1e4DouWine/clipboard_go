package uiapp

import (
	"my-go-app/i18n"
	"my-go-app/store"
)

// harness 是视图测试用的最小装配：状态 + 绑定指针 + 记录到的动作。
//
// 它替代了重构前"直接构造 app 再渲染"的写法：视图层只依赖
// Snapshot / Config / Actions 三样东西，测试因此可以在不碰应用层的前提下
// 覆盖渲染与交互。
type harness struct {
	items        []store.Entry
	chosen       int
	status       string
	loading      bool
	locale       i18n.Locale
	query        string
	filter       int
	themeIdx     int
	langIdx      int
	autoStart    bool
	settingsOpen bool

	events []string
}

// snapshot 返回当前状态快照。
func (h *harness) snapshot() State {
	return State{Items: h.items, Chosen: h.chosen, Status: h.status, Loading: h.loading, Locale: h.locale}
}

// presenter 构造绑定到本 harness 的 Presenter。
func (h *harness) presenter() *Presenter {
	return New(h.snapshot, Config{
		Query:        &h.query,
		Filter:       &h.filter,
		ThemeIdx:     &h.themeIdx,
		LangIdx:      &h.langIdx,
		AutoStart:    &h.autoStart,
		SettingsOpen: &h.settingsOpen,
	}, Actions{
		SearchChanged: func() { h.events = append(h.events, "search") },
		FilterChanged: func() { h.events = append(h.events, "filter") },
		MoveChosen: func(delta int) {
			h.chosen = max(0, min(h.chosen+delta, len(h.items)-1))
			h.events = append(h.events, "move")
		},
		Activate:        func(i int) { h.events = append(h.events, "activate") },
		DeleteChosen:    func() { h.events = append(h.events, "delete") },
		ToggleFavorite:  func() { h.events = append(h.events, "favorite") },
		ToggleAutoStart: func(on bool) { h.events = append(h.events, "autostart") },
		ThemeChanged:    func(idx int) { h.events = append(h.events, "theme") },
		LangChanged:     func(idx int) { h.events = append(h.events, "lang") },
		HideWindow:      func() { h.events = append(h.events, "hide") },
		Quit:            func() { h.events = append(h.events, "quit") },
	})
}

// hasEvent 报告动作流中是否出现指定事件。
func (h *harness) hasEvent(name string) bool {
	for _, e := range h.events {
		if e == name {
			return true
		}
	}
	return false
}
