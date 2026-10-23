package i18n

import (
	"errors"
	"testing"
)

// fakeSettings 是 Settings 的内存测试替身。
type fakeSettings struct {
	values map[string]string
	fail   bool
}

func newFakeSettings() *fakeSettings {
	return &fakeSettings{values: map[string]string{}}
}

func (f *fakeSettings) GetSetting(key string) (string, bool, error) {
	if f.fail {
		return "", false, errors.New("fake: read failed")
	}
	v, ok := f.values[key]
	return v, ok, nil
}

func (f *fakeSettings) SetSetting(key, value string) error {
	if f.fail {
		return errors.New("fake: write failed")
	}
	f.values[key] = value
	return nil
}

func TestLoadSaveLang(t *testing.T) {
	s := newFakeSettings()

	// 未设置 → 默认英文
	if got := Load(s); got != LangEN {
		t.Errorf("Load(空) = %v, want LangEN", got)
	}

	if err := Save(s, LangZH); err != nil {
		t.Fatalf("Save(ZH) 失败: %v", err)
	}
	if got := Load(s); got != LangZH {
		t.Errorf("Load(存 ZH) = %v, want LangZH", got)
	}

	if err := Save(s, LangEN); err != nil {
		t.Fatalf("Save(EN) 失败: %v", err)
	}
	if got := Load(s); got != LangEN {
		t.Errorf("Load(存 EN) = %v, want LangEN", got)
	}

	// 非法值回退英文
	if err := s.SetSetting(SettingsKey, "!!!broken!!!"); err != nil {
		t.Fatalf("SetSetting 非法值失败: %v", err)
	}
	if got := Load(s); got != LangEN {
		t.Errorf("Load(非法) = %v, want LangEN", got)
	}

	// 读取失败回退英文（不阻塞启动）
	s.fail = true
	if got := Load(s); got != LangEN {
		t.Errorf("Load(读取失败) = %v, want LangEN", got)
	}
	s.fail = false

	// nil 存储安全
	if got := Load(nil); got != LangEN {
		t.Errorf("Load(nil) = %v, want LangEN", got)
	}
	if err := Save(nil, LangZH); err == nil {
		t.Error("Save(nil) 应返回错误")
	}
}

func TestTDefaultEnglish(t *testing.T) {
	l := New(LangEN)
	if got := l.T("Clipboard History"); got != "Clipboard History" {
		t.Errorf("T(EN) = %q, want English source", got)
	}
	if got := l.T("No matches"); got != "No matches" {
		t.Errorf("T(EN) = %q, want English source", got)
	}
}

func TestTChinese(t *testing.T) {
	l := New(LangZH)
	if got := l.T("Clipboard History"); got != "剪贴板历史" {
		t.Errorf("T(ZH) = %q, want 剪贴板历史", got)
	}
	if got := l.T("Search history"); got != "搜索历史" {
		t.Errorf("T(ZH) = %q, want 搜索历史", got)
	}
}

func TestTMissingFallsBackToEnglishSource(t *testing.T) {
	l := New(LangZH)
	if got := l.T("This key is not in dict"); got != "This key is not in dict" {
		t.Errorf("missing key = %q, want English source fallback", got)
	}
}

func TestTplFormatsInBothLanguages(t *testing.T) {
	if got := New(LangEN).Tpl("%d items", 3); got != "3 items" {
		t.Errorf("Tpl EN = %q, want %q", got, "3 items")
	}
	if got := New(LangZH).Tpl("%d items", 3); got != "3 条" {
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
	if New(LangEN).Index() != 0 || New(LangZH).Index() != 1 {
		t.Errorf("Index mapping mismatch: EN=%d ZH=%d", New(LangEN).Index(), New(LangZH).Index())
	}
}

func TestParseLang(t *testing.T) {
	cases := map[string]Lang{
		"zh":      LangZH,
		"zh-CN":   LangZH,
		"zh-Hans": LangZH,
		"en":      LangEN,
		"":        LangEN,
		"broken":  LangEN,
	}
	for in, want := range cases {
		if got := ParseLang(in); got != want {
			t.Errorf("ParseLang(%q) = %v, want %v", in, got, want)
		}
	}
}

// TestZeroLocaleIsEnglish 验证零值 Locale 合法（英文），避免未初始化即 panic。
func TestZeroLocaleIsEnglish(t *testing.T) {
	var l Locale
	if l.Lang() != LangEN {
		t.Errorf("零值 Locale 应为英文，实际 %v", l.Lang())
	}
	if got := l.T("Clipboard History"); got != "Clipboard History" {
		t.Errorf("零值 Locale.T = %q, want English source", got)
	}
	if l.Index() != 0 {
		t.Errorf("零值 Locale.Index = %d, want 0", l.Index())
	}
}

// TestIllegalLangFallsBackToEnglish 验证非法 Lang 收敛为英文。
func TestIllegalLangFallsBackToEnglish(t *testing.T) {
	l := New(Lang(42))
	if l.Lang() != LangEN {
		t.Errorf("非法 Lang 应回退英文，实际 %v", l.Lang())
	}
}

func TestLabelsFollowLanguage(t *testing.T) {
	enTheme := New(LangEN).ThemeLabels()
	enFilter := New(LangEN).FilterLabels()
	zhTheme := New(LangZH).ThemeLabels()
	zhFilter := New(LangZH).FilterLabels()

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
