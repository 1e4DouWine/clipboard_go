package main

import (
	"fmt"

	"github.com/egoist/mygo"

	"my-go-app/store"

	// mygo 控件内建文案（Search/Clear 等）的简体中文目录。
	_ "github.com/egoist/mygo/locales/zh-Hans"
)

// translate.go — 界面文案字典（中/英）。
//
// 约定：
//   - 英文源串即 key；T 在中文下查 dict，其余情况原样返回英文（天然回退）。
//   - 格式化文案用 Tpl，中英文共用同一套 %d/%s 参数顺序。
//   - 类型徽章（URL/CODE/IMG/TXT）等技术码不翻译。
//   - lang 全局单例：主线程读写；测试可直接赋值。

// Lang 是界面语言。
type Lang int

const (
	LangEN Lang = iota // English（默认）
	LangZH             // 简体中文
)

// LangSettingsKey 是语言偏好在 store.settings 表中的键。
const LangSettingsKey = "lang"

// lang 是当前界面语言（全局，T 只读）。
var lang = LangEN

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
	"No matches":                        "无匹配结果",
	"Try another keyword or category":   "换个关键词或分类试试",
	"Copy some text anywhere":           "在任意处复制文本",
	"It will appear here automatically": "会自动出现在这里",

	// 状态栏
	"Launch at login": "开机自启",
	"↑↓ Choose  ·  ↵ Paste  ·  Del Delete  ·  Ctrl+D Favorite  ·  Esc Close":
		"↑↓ 选择  ·  ↵ 粘贴  ·  Del 删除  ·  Ctrl+D 收藏  ·  Esc 关闭",

	// 主题
	"Auto":  "自动",
	"Light": "浅色",
	"Dark":  "暗黑",
	"Theme": "主题",

	// 设置页（"English" / "中文" 作为语言名各自保持原文，不进字典）
	"Language": "语言",
	"Close":    "关闭",

	// 状态提示
	"Failed to load history. Check the log.":
		"加载历史失败，请查看日志",
	"Failed to write clipboard: %s":
		"写入剪贴板失败：%s",
	"Copied to clipboard (auto-paste failed, paste manually)":
		"已复制到剪贴板（自动粘贴失败，请手动粘贴）",
	"Failed to delete: %s":
		"删除失败：%s",
	"Action failed: %s":
		"操作失败：%s",
	"Failed to enable launch at login. Check system permissions.":
		"设置开机自启失败，请检查系统权限",
	"Appearance changed (save failed; will reset after restart)":
		"外观已切换（偏好保存失败，重启后恢复默认）",
	"Failed to register global shortcut. Use another shortcut.":
		"全局快捷键注册失败，可改用其他快捷键",
	"UI render error, showing fallback":
		"界面渲染出错，已降级显示",
	"See the log file for details":
		"详情见日志文件",

	// 设置写入失败（语言）
	"Language changed (save failed; will reset after restart)":
		"语言已切换（偏好保存失败，重启后恢复默认）",
}

// T 返回 en 在当前语言下的文案。非中文或无映射时原样返回 en（回退英文）。
func T(en string) string {
	if lang == LangZH {
		if zh, ok := dict[en]; ok {
			return zh
		}
	}
	return en
}

// Tpl 对 T(en) 做 fmt.Sprintf，用于 "%d items" 等格式化文案。
func Tpl(en string, args ...any) string {
	return fmt.Sprintf(T(en), args...)
}

// Index 返回语言在设置页 Segmented 中的序号（0=English，1=中文）。
func (l Lang) Index() int { return int(l) }

// LangOfIndex 把设置页序号映射为 Lang；越界回退英文。
func LangOfIndex(idx int) Lang {
	if idx == int(LangZH) {
		return LangZH
	}
	return LangEN
}

// LoadLang 从 store 读取语言偏好。未设置或非法值 → LangEN（默认英文）。
func LoadLang(st *store.Store) Lang {
	if st == nil {
		return LangEN
	}
	v, ok, err := st.GetSetting(LangSettingsKey)
	if err != nil || !ok {
		return LangEN
	}
	switch v {
	case "zh", "zh-CN", "zh-Hans":
		return LangZH
	case "en", "":
		return LangEN
	default:
		return LangEN
	}
}

// SaveLang 持久化语言偏好（存 "en" / "zh"）。
func SaveLang(st *store.Store, l Lang) error {
	if st == nil {
		return fmt.Errorf("translate: store 不能为空")
	}
	v := "en"
	if l == LangZH {
		v = "zh"
	}
	return st.SetSetting(LangSettingsKey, v)
}

// applyMygoLocale 同步 mygo 控件内建文案（Search 占位符等）到当前语言。
// 应用文案仍由 translate.go 的 T 管辖；此处只对齐控件 chrome。
// 仅在 App 就绪后调用（测试不写 locale 文件）。
func applyMygoLocale() {
	if !mygo.App.IsReady() {
		return
	}
	tag := "en"
	if lang == LangZH {
		tag = "zh-Hans"
	}
	mygo.App.SetLocale(tag)
}

// themeModeLabels 返回主题切换器标签（随语言变化）。
func themeModeLabels() []string {
	return []string{T("Auto"), T("Light"), T("Dark")}
}

// filterLabelList 返回分类过滤器标签（随语言变化）。
func filterLabelList() []string {
	return []string{T("All"), T("Text"), T("Links"), T("Code"), T("Images"), T("Favorites")}
}
