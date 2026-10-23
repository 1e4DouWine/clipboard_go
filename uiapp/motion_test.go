package uiapp

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"my-go-app/store"
)

// TestRowPoseStates 验证卡片三态姿态参数（悬停上浮/按压缩小/默认归位）。
func TestRowPoseStates(t *testing.T) {
	// 默认：零姿态。
	if p := rowPose(false, false); p != (ui.Pose{}) {
		t.Fatalf("默认姿态应为零值，实际 %+v", p)
	}
	// 悬停：上浮 1.5DIP + 微放大。
	p := rowPose(true, false)
	if p.Y != -1.5 || p.Scale != 1.005 {
		t.Fatalf("悬停姿态应上浮微放大，实际 %+v", p)
	}
	// 按压优先于悬停：轻微缩小。
	p = rowPose(true, true)
	if p.Scale != 0.985 || p.Y != 0 {
		t.Fatalf("按压姿态应缩小归位，实际 %+v", p)
	}
}

// TestStarPose 验证收藏星弹跳姿态。
func TestStarPose(t *testing.T) {
	if p := starPose(false); p.Scale != 0 {
		t.Fatalf("未收藏应为原尺寸（Scale 0 表示保持），实际 %+v", p)
	}
	if p := starPose(true); p.Scale != 1.25 {
		t.Fatalf("收藏应放大 1.25，实际 %+v", p)
	}
}

// TestViewWithAnimations 验证带动画的视图正常渲染（文本不丢失）。
func TestViewWithAnimations(t *testing.T) {
	h := &harness{items: []store.Entry{
		{ID: 1, Kind: store.KindText, Preview: "anim one", CopiedAt: 1700000000000, IsFavorite: true},
		{ID: 2, Kind: store.KindLink, Preview: "https://anim.dev", CopiedAt: 1700000000000},
	}}
	tt := ui.NewTester(h.presenter().View, 560, 520)
	// 动画不改变内容语义：预览、徽标、星标均应渲染。
	for _, want := range []string{"anim one", "https://anim.dev", "URL", "★", "☆"} {
		if !tt.HasText(want) {
			t.Errorf("expected %q in texts %q", want, tt.Texts())
		}
	}
}

// TestViewAnimationsReduceMotion 验证"减少动态效果"降级路径：
// 系统开启 ReduceMotion 时 mygo 动画自动降级，视图必须照常渲染不丢内容。
func TestViewAnimationsReduceMotion(t *testing.T) {
	h := &harness{
		items: []store.Entry{
			{ID: 1, Kind: store.KindText, Preview: "calm entry", CopiedAt: 1700000000000},
		},
		status: "已复制",
	}
	tt := ui.NewTester(h.presenter().View, 560, 520)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true, TextScale: 1})

	// 重建一帧：降级路径下内容完整。
	if !tt.HasText("calm entry") {
		t.Errorf("ReduceMotion 下条目应正常渲染, texts %q", tt.Texts())
	}
	if !tt.HasText("已复制") {
		t.Errorf("ReduceMotion 下状态提示应正常渲染, texts %q", tt.Texts())
	}
}

// TestViewStatusFade 验证状态栏淡入在有/无状态时均正常。
func TestViewStatusFade(t *testing.T) {
	// 有状态：状态文案渲染。
	h := &harness{status: "粘贴成功"}
	tt := ui.NewTester(h.presenter().View, 560, 520)
	if !tt.HasText("粘贴成功") {
		t.Errorf("expected status text, texts %q", tt.Texts())
	}
	// 无状态：快捷键帮助渲染。
	b := &harness{}
	tt2 := ui.NewTester(b.presenter().View, 560, 520)
	if !tt2.HasText("↑↓ Choose") {
		t.Errorf("expected shortcuts hint, texts %q", tt2.Texts())
	}
}

// TestChipColors 验证类型徽标配色策略（语义色 + 半透明底，明暗主题自动适配）。
func TestChipColors(t *testing.T) {
	th := ui.LightTheme()
	// URL 用 Accent、CODE 用 Warning、IMG 用 Success、TXT 用 TextMuted。
	cases := map[store.Kind]ui.Color{
		store.KindLink:  th.Accent,
		store.KindCode:  th.Warning,
		store.KindImage: th.Success,
		store.KindText:  th.TextMuted,
	}
	for kind, want := range cases {
		fg, bg := chipColors(th, kind)
		if fg != want {
			t.Errorf("chipColors(%q) 前景色 = %v，期望 %v", kind, fg, want)
		}
		if bg.A == 0 {
			t.Errorf("chipColors(%q) 背景色不应全透明", kind)
		}
	}
	// 暗黑主题下同样返回非零配色（自动适配验证）。
	dark := ui.DarkTheme()
	if fg, bg := chipColors(dark, store.KindLink); fg != dark.Accent || bg.A == 0 {
		t.Error("暗黑主题下 chip 配色未适配")
	}
}

// TestKindBadgeAndMeta 验证徽标文案与元信息行。
func TestKindBadgeAndMeta(t *testing.T) {
	if got := kindBadge(store.KindImage); got != "IMG" {
		t.Fatalf("kindBadge(IMG) = %q", got)
	}
	if got := kindBadge(store.KindText); got != "TXT" {
		t.Fatalf("kindBadge(TXT) = %q", got)
	}
	e := store.Entry{CopiedAt: 1700000000000, SourceApp: "VS Code"}
	if got := metaLine(e); got == "" {
		t.Fatal("metaLine 不应为空")
	}
}
