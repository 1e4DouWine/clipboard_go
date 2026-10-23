package uiapp

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"

	"my-go-app/i18n"
	"my-go-app/store"
)

// TestDumpFluentVisuals 导出明/暗两种外观下的界面截图，供人工视觉验收。
// 仅在设置 FLUENT_DUMP=dir 时运行；否则跳过，避免常规 go test 写文件。
func TestDumpFluentVisuals(t *testing.T) {
	dir := os.Getenv("FLUENT_DUMP")
	if dir == "" {
		t.Skip("set FLUENT_DUMP=dir to export Fluent UI screenshots")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	samples := []store.Entry{
		{ID: 1, Kind: store.KindText, Preview: "Meeting notes: ship Fluent polish this week", SourceApp: "Notepad", CopiedAt: 1700000000000},
		{ID: 2, Kind: store.KindLink, Preview: "https://learn.microsoft.com/windows/apps/design", SourceApp: "Chrome", CopiedAt: 1700000100000},
		{ID: 3, Kind: store.KindCode, Preview: "ui.Column(c).Fill().Gap(0)", SourceApp: "Code", CopiedAt: 1700000200000, IsFavorite: true},
		{ID: 4, Kind: store.KindImage, Preview: "screenshot.png", SourceApp: "Snipping Tool", CopiedAt: 1700000300000},
	}

	cases := []struct {
		name string
		dark bool
		lang i18n.Lang
		h    *harness
	}{
		{name: "light-list", dark: false, lang: i18n.LangEN, h: &harness{items: samples, chosen: 1}},
		{name: "dark-list", dark: true, lang: i18n.LangEN, h: &harness{items: samples, chosen: 1}},
		{name: "light-empty", dark: false, lang: i18n.LangEN, h: &harness{}},
		{name: "light-filtered", dark: false, lang: i18n.LangEN, h: &harness{query: "zzz"}},
		{name: "light-settings-en", dark: false, lang: i18n.LangEN, h: &harness{items: samples, settingsOpen: true, themeIdx: 1, langIdx: 0, autoStart: true}},
		{name: "dark-settings-zh", dark: true, lang: i18n.LangZH, h: &harness{items: samples, settingsOpen: true, themeIdx: 2, langIdx: 1}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.h.locale = i18n.New(tc.lang)
			tt := ui.NewTester(tc.h.presenter().View, 560, 520)
			tt.SetDark(tc.dark)
			tt.Frame()
			img := tt.Image()
			path := filepath.Join(dir, tc.name+".png")
			f, err := os.Create(path)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if err := png.Encode(f, img); err != nil {
				t.Fatal(err)
			}
			t.Logf("wrote %s (%dx%d)", path, img.Bounds().Dx(), img.Bounds().Dy())
		})
	}
}
