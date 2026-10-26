package uiapp

import (
	"github.com/egoist/mygo/ui"
)

// settingsWidth 是设置对话框内容宽度（DIP）。
const settingsWidth = 340

// settingsModal 渲染设置对话框（ui.Modal：遮罩/Esc/关闭钮均可关）。
//
// 布局：标题行 + 若干"左标签 / 右控件"设置行，行间以发丝线分隔。
//
// Modal 的开关指针就是模型字段本身：Modal 在构建阶段可能把它置 false
// （Esc 关闭），因此视图对它的读写都是直接写模型，不做缓存。
func (p *Presenter) settingsModal(c *ui.Context, t *ui.Theme) {
	ui.Modal(c.Key("settings-modal"), p.Config.SettingsOpen, func() {
		ui.Column(c).Width(settingsWidth).Gap(t.Space(3)).Children(func() {
			// 标题行
			ui.Row(c.Key("settings-title")).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
				ui.Text(c, p.t("Settings")).FontSize(t.Rem(typeTitle)).FontWeight(600).Grow(1)
				iconButton(c, t, "settings-close", closeIcon, p.t("Close"), func() {
					*p.Config.SettingsOpen = false
				})
			})

			ui.Column(c.Key("settings-rows")).Dividers(1, t.Border.Alpha(0.6)).Children(func() {
				// 语言
				p.settingRow(c, t, "settings-lang", "Language", func() {
					if ui.Segmented(c.Key("lang"), p.Config.LangIdx, "English", "中文").
						Label(p.t("Language")).Changed() {
						p.langChanged(*p.Config.LangIdx)
					}
				})
				// 主题
				p.settingRow(c, t, "settings-theme", "Theme", func() {
					if ui.Segmented(c.Key("theme"), p.Config.ThemeIdx, p.loc().ThemeLabels()...).
						Label(p.t("Theme")).Changed() {
						p.themeChanged(*p.Config.ThemeIdx)
					}
				})
				// 开机自启
				p.settingRow(c, t, "settings-autostart", "Launch at login", func() {
					if ui.Switch(c.Key("autostart"), p.Config.AutoStart).
						Label(p.t("Launch at login")).Changed() {
						p.toggleAutoStart(*p.Config.AutoStart)
					}
				})
			})
		})
	})
}

// settingRow 渲染一行设置：左侧标签（英文源串，经翻译），右侧控件。
func (p *Presenter) settingRow(c *ui.Context, t *ui.Theme, key, label string, control func()) {
	ui.Row(c.Key(key)).PaddingY(t.Space(2.5)).Gap(t.Space(3)).AlignItems(ui.Center).Children(func() {
		ui.Text(c, p.t(label)).FontSize(t.Rem(typeBody)).TextColor(t.Text).Grow(1)
		control()
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
