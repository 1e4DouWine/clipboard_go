package uiapp

import (
	"github.com/egoist/mygo/ui"
)

// settingsModal 渲染设置对话框（ui.Modal：遮罩/Esc/关闭钮均可关）。
//
// Modal 的开关指针就是模型字段本身：Modal 在构建阶段可能把它置 false
// （Esc 关闭），因此视图对它的读写都是直接写模型，不做缓存。
func (p *Presenter) settingsModal(c *ui.Context, t *ui.Theme) {
	ui.Modal(c.Key("settings-modal"), p.Config.SettingsOpen, func() {
		ui.Column(c).Gap(t.Space(3)).Children(func() {
			// 标题行
			ui.Row(c.Key("settings-title")).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
				ui.Text(c, p.t("Settings")).FontSize(t.Rem(typeTitle)).Bold().Grow(1)
				closeBtn := ui.ButtonBase(c.Key("settings-close")).
					Label(p.t("Close")).Tooltip(p.t("Close"))
				closeBtn.Padding(t.Space(1.25)).Radius(t.Radius)
				if closeBtn.Hovered() {
					closeBtn.Background(t.SurfaceHover)
				}
				closeBtn.OnClick(func() {
					*p.Config.SettingsOpen = false
				}).Children(func() {
					ui.Icon(c, closeIcon).FontSize(t.Rem(1)).TextColor(t.TextMuted)
				})
			})

			// 语言
			ui.Column(c.Key("settings-lang")).Gap(t.Space(1.5)).Children(func() {
				ui.Text(c, p.t("Language")).FontSize(t.Rem(typeCaption)).TextColor(t.TextMuted)
				if ui.Segmented(c.Key("lang"), p.Config.LangIdx, "English", "中文").
					Label(p.t("Language")).Changed() {
					p.langChanged(*p.Config.LangIdx)
				}
			})

			// 主题（自标题栏迁入）
			ui.Column(c.Key("settings-theme")).Gap(t.Space(1.5)).Children(func() {
				ui.Text(c, p.t("Theme")).FontSize(t.Rem(typeCaption)).TextColor(t.TextMuted)
				if ui.Segmented(c.Key("theme"), p.Config.ThemeIdx, p.loc().ThemeLabels()...).
					Label(p.t("Theme")).Changed() {
					p.themeChanged(*p.Config.ThemeIdx)
				}
			})

			// 开机自启（自状态栏迁入）
			if ui.Checkbox(c.Key("autostart"), p.Config.AutoStart, p.t("Launch at login")).
				Label(p.t("Launch at login")).Changed() {
				p.toggleAutoStart(*p.Config.AutoStart)
			}
		})
	})
}

// langChanged 通知应用层切换界面语言。
func (p *Presenter) langChanged(idx int) {
	p.Actions.LangChanged(idx)
}

// themeChanged 通知应用层切换外观模式。
func (p *Presenter) themeChanged(idx int) {
	p.Actions.ThemeChanged(idx)
}

// toggleAutoStart 通知应用层设置开机自启。
func (p *Presenter) toggleAutoStart(on bool) {
	p.Actions.ToggleAutoStart(on)
}
