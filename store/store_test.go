package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// newTestStore 创建一个基于临时目录的 Store，测试结束后自动关闭。
func newTestStore(t *testing.T, opts Options) *Store {
	t.Helper()
	s, err := Open(t.TempDir(), opts)
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// TestAddTextDedup 验证相同内容重复复制走"置顶不新增"语义。
func TestAddTextDedup(t *testing.T) {
	s := newTestStore(t, Options{})

	first, err := s.AddText("hello world", "VSCode")
	if err != nil {
		t.Fatalf("首次 AddText 失败: %v", err)
	}
	second, err := s.AddText("hello world", "Chrome")
	if err != nil {
		t.Fatalf("重复 AddText 失败: %v", err)
	}

	// 不应新增条目，且 ID 与创建时间保持不变（仅刷新 copied_at / source_app）。
	if first.ID != second.ID {
		t.Fatalf("重复内容产生了新条目: first.ID=%d second.ID=%d", first.ID, second.ID)
	}
	if first.CreatedAt != second.CreatedAt {
		t.Fatalf("创建时间不应变化: %d != %d", first.CreatedAt, second.CreatedAt)
	}
	if second.SourceApp != "Chrome" {
		t.Fatalf("来源应用未刷新: %q", second.SourceApp)
	}

	items, err := s.List(0)
	if err != nil {
		t.Fatalf("List 失败: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("期望 1 条记录，实际 %d 条", len(items))
	}
}

// TestAddTextEmpty 验证空白文本被拒绝。
func TestAddTextEmpty(t *testing.T) {
	s := newTestStore(t, Options{})
	for _, in := range []string{"", "   ", "\n\t "} {
		if _, err := s.AddText(in, ""); !errors.Is(err, ErrEmptyContent) {
			t.Fatalf("输入 %q 期望 ErrEmptyContent，实际: %v", in, err)
		}
	}
}

// TestAddTextTruncatesLong 验证超长文本截断入库且 UTF-8 尾部完整。
func TestAddTextTruncatesLong(t *testing.T) {
	s := newTestStore(t, Options{})

	// 构造恰好越过上限、且截断点落在多字节字符中间的内容。
	long := strings.Repeat("a", MaxTextBytes-1) + "世" + strings.Repeat("b", 100)
	e, err := s.AddText(long, "")
	if err != nil {
		t.Fatalf("AddText 失败: %v", err)
	}

	if len(e.FullText) > MaxTextBytes {
		t.Fatalf("截断后仍超上限: %d 字节", len(e.FullText))
	}
	// 截断不得把 "世" 切成半个字符。
	if !utf8.ValidString(e.FullText) {
		t.Fatal("截断产生了非法 UTF-8 序列")
	}
	if !strings.HasSuffix(e.Preview, "（已截断）") {
		t.Fatalf("预览应带截断标记，实际: %q", e.Preview)
	}
}

// TestDetectKind 验证内容分类启发式。
func TestDetectKind(t *testing.T) {
	cases := []struct {
		in   string
		want Kind
	}{
		{"https://example.com/path", KindLink},
		{"www.example.com", KindLink},
		{"HTTP://EXAMPLE.COM", KindLink},
		{"see https://example.com now", KindText}, // 非单行/非开头，不判链接
		{"func main() {\n\tfmt.Println()\n}", KindCode},
		{"你好世界", KindText},
		{"", KindText},
	}
	for _, c := range cases {
		if got := DetectKind(c.in); got != c.want {
			t.Errorf("DetectKind(%q) = %q，期望 %q", c.in, got, c.want)
		}
	}
}

// TestPurgeKeepsFavorite 验证容量淘汰永远保留收藏条目。
func TestPurgeKeepsFavorite(t *testing.T) {
	s := newTestStore(t, Options{MaxEntries: 3})

	a, err := s.AddText("a", "")
	if err != nil {
		t.Fatalf("AddText 失败: %v", err)
	}
	for _, txt := range []string{"b", "c"} {
		if _, err := s.AddText(txt, ""); err != nil {
			t.Fatalf("AddText(%q) 失败: %v", txt, err)
		}
	}
	// 在触发淘汰前，把最旧的 a 收藏起来（它应在此后的淘汰中幸存）。
	if _, err := s.ToggleFavorite(a.ID); err != nil {
		t.Fatalf("ToggleFavorite 失败: %v", err)
	}
	// 继续写入触发淘汰：超出部分应从最旧的"非收藏"条目开始删除。
	for _, txt := range []string{"d", "e", "f"} {
		if _, err := s.AddText(txt, ""); err != nil {
			t.Fatalf("AddText(%q) 失败: %v", txt, err)
		}
	}

	items, err := s.List(0)
	if err != nil {
		t.Fatalf("List 失败: %v", err)
	}
	if len(items) > 3 {
		t.Fatalf("超出容量上限: %d 条", len(items))
	}
	found := false
	for _, it := range items {
		if it.ID == a.ID {
			found = true
			if !it.IsFavorite {
				t.Fatal("收藏条目丢失收藏标记")
			}
		}
	}
	if !found {
		t.Fatal("收藏条目被容量淘汰")
	}
}

// TestSearch 验证搜索：大小写不敏感、LIKE 通配符转义、空查询回退全量。
func TestSearch(t *testing.T) {
	s := newTestStore(t, Options{})

	if _, err := s.AddText("progress 100% done", ""); err != nil {
		t.Fatalf("AddText 失败: %v", err)
	}
	if _, err := s.AddText("progress 100x done", ""); err != nil {
		t.Fatalf("AddText 失败: %v", err)
	}

	got, err := s.Search("100%", 0)
	if err != nil {
		t.Fatalf("Search 失败: %v", err)
	}
	if len(got) != 1 || !strings.Contains(got[0].FullText, "100%") {
		t.Fatalf("通配符未按字面量转义，结果: %+v", got)
	}

	got, err = s.Search("PROGRESS", 0)
	if err != nil {
		t.Fatalf("Search 失败: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("大小写不敏感搜索应命中 2 条，实际 %d 条", len(got))
	}

	// 空查询回退全量。
	got, err = s.Search("   ", 0)
	if err != nil {
		t.Fatalf("Search(空) 失败: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("空查询应回退全量 2 条，实际 %d 条", len(got))
	}
}

// TestImageRoundtrip 验证图片落盘、去重、读取与删除。
func TestImageRoundtrip(t *testing.T) {
	s := newTestStore(t, Options{})

	data := []byte("fake-image-bytes") // 解码失败时预览降级，不应影响入库
	e, err := s.AddImage(data, "Paint")
	if err != nil {
		t.Fatalf("AddImage 失败: %v", err)
	}
	if e.Kind != KindImage {
		t.Fatalf("类型应为 image，实际 %q", e.Kind)
	}

	abs := s.ImageAbsPath(e.ImagePath)
	if abs == "" {
		t.Fatal("图片绝对路径解析失败")
	}
	if _, err := os.Stat(abs); err != nil {
		t.Fatalf("图片文件不存在: %v", err)
	}

	// 相同图片重复添加不新增条目。
	again, err := s.AddImage(data, "Paint")
	if err != nil {
		t.Fatalf("重复 AddImage 失败: %v", err)
	}
	if again.ID != e.ID {
		t.Fatalf("重复图片产生了新条目: %d != %d", again.ID, e.ID)
	}

	// ReadImage 往返一致。
	read, err := s.ReadImage(e)
	if err != nil {
		t.Fatalf("ReadImage 失败: %v", err)
	}
	if string(read) != string(data) {
		t.Fatal("图片内容不一致")
	}

	// 删除条目后文件同步清理。
	if err := s.Delete(e.ID); err != nil {
		t.Fatalf("Delete 失败: %v", err)
	}
	if _, err := os.Stat(abs); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("删除条目后图片文件仍存在: %v", err)
	}
}

// TestAddImageBoundary 验证图片大小边界。
func TestAddImageBoundary(t *testing.T) {
	s := newTestStore(t, Options{})

	if _, err := s.AddImage(nil, ""); !errors.Is(err, ErrEmptyContent) {
		t.Fatalf("空图片期望 ErrEmptyContent，实际: %v", err)
	}
	big := make([]byte, MaxImageBytes+1)
	if _, err := s.AddImage(big, ""); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("超大图片期望 ErrTooLarge，实际: %v", err)
	}
}

// TestToggleFavoriteAndMissing 验证收藏切换与不存在条目的错误语义。
func TestToggleFavoriteAndMissing(t *testing.T) {
	s := newTestStore(t, Options{})

	e, err := s.AddText("fav", "")
	if err != nil {
		t.Fatalf("AddText 失败: %v", err)
	}
	if fav, err := s.ToggleFavorite(e.ID); err != nil || !fav {
		t.Fatalf("首次切换应为收藏: fav=%v err=%v", fav, err)
	}
	if fav, err := s.ToggleFavorite(e.ID); err != nil || fav {
		t.Fatalf("二次切换应取消收藏: fav=%v err=%v", fav, err)
	}
	if _, err := s.ToggleFavorite(999999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("不存在的 ID 期望 ErrNotFound，实际: %v", err)
	}
	if err := s.Delete(999999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("删除不存在的 ID 期望 ErrNotFound，实际: %v", err)
	}
}

// TestClearKeepFavorites 验证清空历史时收藏保护。
func TestClearKeepFavorites(t *testing.T) {
	s := newTestStore(t, Options{})

	keep, err := s.AddText("keep", "")
	if err != nil {
		t.Fatalf("AddText 失败: %v", err)
	}
	if _, err := s.AddText("drop", ""); err != nil {
		t.Fatalf("AddText 失败: %v", err)
	}
	if _, err := s.ToggleFavorite(keep.ID); err != nil {
		t.Fatalf("ToggleFavorite 失败: %v", err)
	}

	if err := s.Clear(true); err != nil {
		t.Fatalf("Clear 失败: %v", err)
	}
	items, err := s.List(0)
	if err != nil {
		t.Fatalf("List 失败: %v", err)
	}
	if len(items) != 1 || items[0].ID != keep.ID {
		t.Fatalf("收藏保护失效，剩余: %+v", items)
	}
}

// TestSettings 验证配置项读写与缺失语义。
func TestSettings(t *testing.T) {
	s := newTestStore(t, Options{})

	if _, ok, err := s.GetSetting("hotkey"); err != nil || ok {
		t.Fatalf("缺失配置应返回 ok=false: ok=%v err=%v", ok, err)
	}
	if err := s.SetSetting("hotkey", "ctrl+shift+v"); err != nil {
		t.Fatalf("SetSetting 失败: %v", err)
	}
	// upsert：重复写覆盖旧值。
	if err := s.SetSetting("hotkey", "ctrl+alt+v"); err != nil {
		t.Fatalf("SetSetting 覆盖失败: %v", err)
	}
	v, ok, err := s.GetSetting("hotkey")
	if err != nil || !ok || v != "ctrl+alt+v" {
		t.Fatalf("读取配置不一致: v=%q ok=%v err=%v", v, ok, err)
	}
	if err := s.SetSetting("", "x"); err == nil {
		t.Fatal("空 key 应报错")
	}
}

// TestCorruptDBRecovery 验证数据库损坏时备份重建，应用仍可用。
func TestCorruptDBRecovery(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, dbFileName)
	// 写入明显不是 SQLite 文件的垃圾数据。
	if err := os.WriteFile(dbPath, []byte("this is not a sqlite database"), 0o644); err != nil {
		t.Fatalf("准备损坏数据库失败: %v", err)
	}

	s, err := Open(dir, Options{})
	if err != nil {
		t.Fatalf("损坏数据库应自动恢复，Open 却失败: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	// 损坏文件应被隔离备份（*.corrupt-*）。
	matches, _ := filepath.Glob(dbPath + ".corrupt-*")
	if len(matches) == 0 {
		t.Fatal("损坏数据库未被备份隔离")
	}

	// 恢复后应可正常写读。
	if _, err := s.AddText("after recovery", ""); err != nil {
		t.Fatalf("恢复后写入失败: %v", err)
	}
}

// TestBriefQueries 验证 Brief 查询不加载全文，全文可按需 Get。
// （内存优化回归测试：列表/搜索是 UI 高频路径，全文必须走懒加载。）
func TestBriefQueries(t *testing.T) {
	s := newTestStore(t, Options{})
	added, err := s.AddText("brief full text content", "")
	if err != nil {
		t.Fatalf("AddText 失败: %v", err)
	}

	// ListBrief：预览在、全文空。
	items, err := s.ListBrief(0)
	if err != nil {
		t.Fatalf("ListBrief 失败: %v", err)
	}
	if len(items) != 1 || items[0].FullText != "" || items[0].Preview == "" {
		t.Fatalf("ListBrief 不应含全文，实际 %+v", items)
	}

	// SearchBrief：全文列参与匹配但不取回。
	items, err = s.SearchBrief("full text", 0)
	if err != nil {
		t.Fatalf("SearchBrief 失败: %v", err)
	}
	if len(items) != 1 || items[0].FullText != "" {
		t.Fatalf("SearchBrief 不应含全文，实际 %+v", items)
	}

	// 按需取全文。
	full, err := s.Get(added.ID)
	if err != nil || full.FullText != "brief full text content" {
		t.Fatalf("按需取全文失败: %+v err=%v", full, err)
	}

	// 空查询回退全量语义一致。
	items, err = s.SearchBrief("   ", 0)
	if err != nil || len(items) != 1 {
		t.Fatalf("SearchBrief 空查询应回退全量: %+v err=%v", items, err)
	}
}

// TestListFilteredPushesFiltersDown 验证过滤在 SQL 层完成：
// LIMIT 作用于过滤之后的结果。
//
// 这正是老问题的修复点：先取"最新 N 条"再在内存里按类型过滤时，
// 若最新 N 条里恰好没有图片，图片分类就会显示为空——更早的图片永远看不到。
func TestListFilteredPushesFiltersDown(t *testing.T) {
	s := newTestStore(t, Options{})

	// 5 条文本 + 3 条图片（图片内容各不相同，避免去重合并）。
	for i := 0; i < 5; i++ {
		if _, err := s.AddText("plain text "+string(rune('a'+i)), "app"); err != nil {
			t.Fatalf("AddText 失败: %v", err)
		}
	}
	for i := 0; i < 3; i++ {
		if _, err := s.AddImage([]byte{byte(i), 0x89, 'P', 'N', 'G'}, "app"); err != nil {
			t.Fatalf("AddImage 失败: %v", err)
		}
	}

	// 只看图片，limit=2：应返回 2 张图片（而非"最新 2 条里没有图片"）。
	imgs, err := s.ListFiltered(Filter{Kind: KindImage}, 2)
	if err != nil {
		t.Fatalf("ListFiltered 失败: %v", err)
	}
	if len(imgs) != 2 {
		t.Fatalf("图片过滤应返回 2 条，实际 %d", len(imgs))
	}
	for _, e := range imgs {
		if e.Kind != KindImage {
			t.Fatalf("应只返回图片，实际 %+v", e)
		}
	}

	// 只看文本，limit=2：应返回 2 条文本。
	texts, err := s.ListFiltered(Filter{Kind: KindText}, 2)
	if err != nil {
		t.Fatalf("ListFiltered 失败: %v", err)
	}
	if len(texts) != 2 {
		t.Fatalf("文本过滤应返回 2 条，实际 %d", len(texts))
	}
	for _, e := range texts {
		if e.Kind != KindText {
			t.Fatalf("应只返回文本，实际 %+v", e)
		}
	}
}

// TestListFilteredCombinesConditions 验证关键字、类型、收藏三个条件可叠加。
func TestListFilteredCombinesConditions(t *testing.T) {
	s := newTestStore(t, Options{})

	if _, err := s.AddText("alpha plain", "app"); err != nil {
		t.Fatalf("AddText 失败: %v", err)
	}
	link, err := s.AddText("https://example.com/alpha", "app")
	if err != nil {
		t.Fatalf("AddText 失败: %v", err)
	}
	if _, err := s.AddText("beta plain", "app"); err != nil {
		t.Fatalf("AddText 失败: %v", err)
	}
	if _, err := s.ToggleFavorite(link.ID); err != nil {
		t.Fatalf("ToggleFavorite 失败: %v", err)
	}

	// 关键字 + 链接类型：只剩那一条链接。
	items, err := s.ListFiltered(Filter{Text: "alpha", Kind: KindLink}, 0)
	if err != nil {
		t.Fatalf("ListFiltered 失败: %v", err)
	}
	if len(items) != 1 || items[0].ID != link.ID {
		t.Fatalf("关键字+类型应只剩 1 条链接，实际 %+v", items)
	}

	// 关键字 + 收藏：只剩被收藏的那条。
	items, err = s.ListFiltered(Filter{Text: "alpha", FavoritesOnly: true}, 0)
	if err != nil {
		t.Fatalf("ListFiltered 失败: %v", err)
	}
	if len(items) != 1 || items[0].ID != link.ID {
		t.Fatalf("关键字+收藏应只剩 1 条，实际 %+v", items)
	}

	// 关键字命中但类型不符：应为空（条件是 AND，不是 OR）。
	items, err = s.ListFiltered(Filter{Text: "beta", Kind: KindLink}, 0)
	if err != nil {
		t.Fatalf("ListFiltered 失败: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("类型不符时应为空，实际 %+v", items)
	}
}

// TestClosedStore 验证 Close 之后调用返回 ErrClosed，且 Close 幂等。
func TestClosedStore(t *testing.T) {
	s := newTestStore(t, Options{})
	if err := s.Close(); err != nil {
		t.Fatalf("Close 失败: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("重复 Close 应幂等: %v", err)
	}
	if _, err := s.AddText("x", ""); !errors.Is(err, ErrClosed) {
		t.Fatalf("关闭后写入期望 ErrClosed，实际: %v", err)
	}
}
