package main

import (
	"testing"

	"my-go-app/store"
)

func TestLoadSaveLang(t *testing.T) {
	st, err := store.Open(t.TempDir(), store.Options{})
	if err != nil {
		t.Fatalf("Open store 失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	// 未设置 → 默认英文
	if got := LoadLang(st); got != LangEN {
		t.Errorf("LoadLang(空) = %v, want LangEN", got)
	}

	if err := SaveLang(st, LangZH); err != nil {
		t.Fatalf("SaveLang(ZH) 失败: %v", err)
	}
	if got := LoadLang(st); got != LangZH {
		t.Errorf("LoadLang(存 ZH) = %v, want LangZH", got)
	}

	if err := SaveLang(st, LangEN); err != nil {
		t.Fatalf("SaveLang(EN) 失败: %v", err)
	}
	if got := LoadLang(st); got != LangEN {
		t.Errorf("LoadLang(存 EN) = %v, want LangEN", got)
	}

	// 非法值回退英文
	if err := st.SetSetting(LangSettingsKey, "!!!broken!!!"); err != nil {
		t.Fatalf("SetSetting 非法值失败: %v", err)
	}
	if got := LoadLang(st); got != LangEN {
		t.Errorf("LoadLang(非法) = %v, want LangEN", got)
	}

	// nil store 安全
	if got := LoadLang(nil); got != LangEN {
		t.Errorf("LoadLang(nil) = %v, want LangEN", got)
	}
	if err := SaveLang(nil, LangZH); err == nil {
		t.Error("SaveLang(nil) 应返回错误")
	}
}

func TestTDefaultEnglish(t *testing.T) {
	lang = LangEN
	defer func() { lang = LangEN }()

	if got := T("Clipboard History"); got != "Clipboard History" {
		t.Errorf("T(EN) = %q, want English source", got)
	}
	if got := T("No matches"); got != "No matches" {
		t.Errorf("T(EN) = %q, want English source", got)
	}
}

func TestTChinese(t *testing.T) {
	lang = LangZH
	defer func() { lang = LangEN }()

	if got := T("Clipboard History"); got != "剪贴板历史" {
		t.Errorf("T(ZH) = %q, want 剪贴板历史", got)
	}
	if got := T("Search history"); got != "搜索历史" {
		t.Errorf("T(ZH) = %q, want 搜索历史", got)
	}
}

func TestTMissingFallsBackToEnglishSource(t *testing.T) {
	lang = LangZH
	defer func() { lang = LangEN }()

	if got := T("This key is not in dict"); got != "This key is not in dict" {
		t.Errorf("missing key = %q, want English source fallback", got)
	}
}

func TestTplFormatsInBothLanguages(t *testing.T) {
	lang = LangEN
	if got := Tpl("%d items", 3); got != "3 items" {
		t.Errorf("Tpl EN = %q, want %q", got, "3 items")
	}
	lang = LangZH
	defer func() { lang = LangEN }()
	if got := Tpl("%d items", 3); got != "3 条" {
		t.Errorf("Tpl ZH = %q, want %q", got, "3 条")
	}
}

func TestLangOfIndex(t *testing.T) {
	cases := []struct {
		idx  int
		want Lang
	}{
		{0, LangEN},
		{1, LangZH},
		{2, LangEN},
		{-1, LangEN},
	}
	for _, tc := range cases {
		if got := LangOfIndex(tc.idx); got != tc.want {
			t.Errorf("LangOfIndex(%d) = %v, want %v", tc.idx, got, tc.want)
		}
	}
	if LangEN.Index() != 0 || LangZH.Index() != 1 {
		t.Errorf("Index mapping mismatch: EN=%d ZH=%d", LangEN.Index(), LangZH.Index())
	}
}

func TestLabelsFollowLanguage(t *testing.T) {
	lang = LangEN
	enTheme := themeModeLabels()
	enFilter := filterLabelList()
	lang = LangZH
	defer func() { lang = LangEN }()
	zhTheme := themeModeLabels()
	zhFilter := filterLabelList()

	if len(enTheme) != 3 || enTheme[0] != "Auto" {
		t.Errorf("theme EN labels = %v", enTheme)
	}
	if zhTheme[0] != "自动" || zhTheme[1] != "浅色" || zhTheme[2] != "暗黑" {
		t.Errorf("theme ZH labels = %v", zhTheme)
	}
	if len(enFilter) != 6 || enFilter[0] != "All" {
		t.Errorf("filter EN labels = %v", enFilter)
	}
	if zhFilter[0] != "全部" || zhFilter[4] != "图片" || zhFilter[5] != "收藏" {
		t.Errorf("filter ZH labels = %v", zhFilter)
	}
}
