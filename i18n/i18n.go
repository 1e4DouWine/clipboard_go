// Package i18n 提供界面文案的中英文本地化。
//
// 约定：
//   - 英文源串即 key；T 在中文下查 dict，其余情况原样返回英文（天然回退）。
//   - 格式化文案用 Tpl，中英文共用同一套 %d/%s 参数顺序。
//   - 技术码（URL/CODE/IMG/TXT 等类型徽章）不翻译。
//
// 语言状态不使用全局变量：Locale 是值类型，随应用状态流转，
// 测试可并行、无需复位全局量。
package i18n

import (
	"fmt"

	"github.com/egoist/mygo"

	// mygo 控件内建文案（Search/Clear 等）的简体中文目录。
	_ "github.com/egoist/mygo/locales/zh-Hans"
)

// Lang 是界面语言。
type Lang int

const (
	LangEN Lang = iota // English（默认）
	LangZH             // 简体中文
)

// SettingsKey 是语言偏好在 settings 表中的键。
const SettingsKey = "lang"

// Locale 是当前语言上下文。零值即英文，可安全用于默认值。
type Locale struct {
	lang Lang
}

// New 创建 Locale；非法语言值回退英文。
func New(lang Lang) Locale { return Locale{lang: normalize(lang)} }

// normalize 把任意 Lang 收敛为合法值（非法值回退英文）。
func normalize(l Lang) Lang {
	if l == LangZH {
		return LangZH
	}
	return LangEN
}

// Lang 返回当前语言。
func (l Locale) Lang() Lang { return normalize(l.lang) }

// Index 返回语言在设置页 Segmented 中的序号（0=English，1=中文）。
func (l Locale) Index() int { return int(normalize(l.lang)) }

// LangOfIndex 把设置页序号映射为 Lang；越界回退英文。
func LangOfIndex(idx int) Lang {
	if idx == int(LangZH) {
		return LangZH
	}
	return LangEN
}

// ParseLang 解析持久化的语言值；空串/未知值回退英文。
func ParseLang(s string) Lang {
	switch s {
	case "zh", "zh-CN", "zh-Hans":
		return LangZH
	default: // "en"、"" 及任何未知值
		return LangEN
	}
}

// dict：英文原文 → 简体中文。缺条目时 T 回退英文源串。
var dict = map[string]string{
	// 窗口 / 标题栏
	"Clipboard History": "剪贴板历史",
	"%d items":          "%d 条",
	"Settings":          "设置",

	// 工具栏 / 过滤
	"Search history": "搜索历史",
	"Filter":         "筛选",
	"All":            "全部",
	"Text":           "文本",
	"Links":          "链接",
	"Code":           "代码",
	"Images":         "图片",
	"Favorites":      "收藏",

	// 空状态
	"Loading history...":                "正在加载历史记录...",
	"No matches":                        "无匹配结果",
	"Try another keyword or category":   "换个关键词或分类试试",
	"Copy some text anywhere":           "在任意处复制文本",
	"It will appear here automatically": "会自动出现在这里",

	// 状态栏
	"Launch at login": "开机自启",
	"↑↓ Choose  ·  ↵ Paste  ·  Del Delete  ·  Ctrl+D Favorite  ·  Esc Close  ·  Ctrl+Q Quit": "↑↓ 选择  ·  ↵ 粘贴  ·  Del 删除  ·  Ctrl+D 收藏  ·  Esc 关闭  ·  Ctrl+Q 退出",

	// 主题
	"Auto":  "自动",
	"Light": "浅色",
	"Dark":  "暗黑",
	"Theme": "主题",

	// 设置页（"English" / "中文" 作为语言名各自保持原文，不进字典）
	"Language": "语言",
	"Close":    "关闭",

	// 状态提示
	"Failed to load history. Check the log.":                      "加载历史失败，请查看日志",
	"Failed to write clipboard: %s":                               "写入剪贴板失败：%s",
	"Copied to clipboard (auto-paste failed, paste manually)":     "已复制到剪贴板（自动粘贴失败，请手动粘贴）",
	"Failed to delete: %s":                                        "删除失败：%s",
	"Action failed: %s":                                           "操作失败：%s",
	"Failed to enable launch at login. Check system permissions.": "设置开机自启失败，请检查系统权限",
	"Appearance changed (save failed; will reset after restart)":  "外观已切换（偏好保存失败，重启后恢复默认）",
	"Failed to register global shortcut. Use another shortcut.":   "全局快捷键注册失败，可改用其他快捷键",
	"Invalid saved shortcut; using default.":                      "已保存的快捷键无效，已使用默认快捷键",

	// 设置写入失败（语言）
	"Language changed (save failed; will reset after restart)": "语言已切换（偏好保存失败，重启后恢复默认）",
}

// T 返回 en 在当前语言下的文案。非中文或无映射时原样返回 en（回退英文）。
func (l Locale) T(en string) string {
	if l.lang == LangZH {
		if zh, ok := dict[en]; ok {
			return zh
		}
	}
	return en
}

// Tpl 对 T(en) 做 fmt.Sprintf，用于 "%d items" 等格式化文案。
func (l Locale) Tpl(en string, args ...any) string {
	return fmt.Sprintf(l.T(en), args...)
}

// ThemeLabels 返回主题切换器标签（随语言变化）。
func (l Locale) ThemeLabels() []string {
	return []string{l.T("Auto"), l.T("Light"), l.T("Dark")}
}

// FilterLabels 返回分类过滤器标签（随语言变化）。
func (l Locale) FilterLabels() []string {
	return []string{l.T("All"), l.T("Text"), l.T("Links"), l.T("Code"), l.T("Images"), l.T("Favorites")}
}

// ApplyMygo 把界面语言同步到 mygo 控件内建文案（Search 占位符等）。
// 应用文案仍由本包管辖；此处只对齐控件 chrome。
// 仅在 App 就绪后调用（测试不写 locale 文件）。
func (l Locale) ApplyMygo() {
	if !mygo.App.IsReady() {
		return
	}
	tag := "en"
	if l.lang == LangZH {
		tag = "zh-Hans"
	}
	mygo.App.SetLocale(tag)
}
