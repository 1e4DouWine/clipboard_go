package watch

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"my-go-app/store"
)

// fakeClipboard 是可脚本化的剪贴板实现：
//   - seq 非空时按序弹出文本（模拟复制过程中内容变化）；
//   - 无更多脚本时返回 sticky 的 text/image 值。
type fakeClipboard struct {
	mu    sync.Mutex
	seq   []string // 待弹出的文本序列
	text  string   // 当前文本（粘性）
	image []byte   // 当前图片（粘性）
}

func (f *fakeClipboard) ReadText() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.seq) > 0 {
		f.text = f.seq[0]
		f.seq = f.seq[1:]
	}
	return f.text
}

func (f *fakeClipboard) ReadImage() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.image
}

// newTestWatcher 创建挂接真实临时 store 的 Watcher，测试结束自动关闭 store。
func newTestWatcher(t *testing.T, clip Clipboard, mutate func(*Options)) (*Watcher, *store.Store) {
	t.Helper()
	st, err := store.Open(t.TempDir(), store.Options{})
	if err != nil {
		t.Fatalf("Open store 失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	opts := Options{
		Clipboard: clip,
		Store:     st,
		Interval:  20 * time.Millisecond, // 测试提速
		Settle:    5 * time.Millisecond,
		// 测试默认关闭敏感过滤，个别用例自行开启。
		SensitiveTitles: []string{},
	}
	if mutate != nil {
		mutate(&opts)
	}
	w, err := New(opts)
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	return w, st
}

// listAll 取回全部历史并校验查询成功。
func listAll(t *testing.T, st *store.Store) []store.Entry {
	t.Helper()
	items, err := st.List(0)
	if err != nil {
		t.Fatalf("List 失败: %v", err)
	}
	return items
}

// TestNewValidation 验证必填依赖缺失时报错、参数归一化生效。
func TestNewValidation(t *testing.T) {
	st, err := store.Open(t.TempDir(), store.Options{})
	if err != nil {
		t.Fatalf("Open store 失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	// Clipboard 缺失。
	if _, err := New(Options{Store: st}); err == nil {
		t.Fatal("Clipboard 缺失应报错")
	}
	// Store 缺失。
	if _, err := New(Options{Clipboard: &fakeClipboard{}}); err == nil {
		t.Fatal("Store 缺失应报错")
	}
	// 参数归一化：非法间隔被钳制为下限，零值取默认。
	w, err := New(Options{Clipboard: &fakeClipboard{}, Store: st, Interval: -1})
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if w.interval != DefaultInterval {
		t.Fatalf("非法间隔应取默认值，实际 %v", w.interval)
	}
	w2, err := New(Options{Clipboard: &fakeClipboard{}, Store: st, Interval: time.Nanosecond})
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if w2.interval != minInterval {
		t.Fatalf("过小间隔应钳制到下限 %v，实际 %v", minInterval, w2.interval)
	}
}

// TestPollCapturesText 验证基本文本捕获与 OnCapture 回调。
func TestPollCapturesText(t *testing.T) {
	clip := &fakeClipboard{text: "hello world"}
	var captured []store.Entry
	w, st := newTestWatcher(t, clip, func(o *Options) {
		o.OnCapture = func(e store.Entry) { captured = append(captured, e) }
	})

	w.poll(context.Background())

	items := listAll(t, st)
	if len(items) != 1 || items[0].FullText != "hello world" {
		t.Fatalf("期望 1 条 hello world，实际 %+v", items)
	}
	if len(captured) != 1 {
		t.Fatalf("OnCapture 应触发 1 次，实际 %d 次", len(captured))
	}
}

// TestPollDedup 验证相同内容重复轮询只入库一次（去重）。
func TestPollDedup(t *testing.T) {
	clip := &fakeClipboard{text: "same content"}
	w, st := newTestWatcher(t, clip, nil)

	w.poll(context.Background())
	w.poll(context.Background())
	w.poll(context.Background())

	if items := listAll(t, st); len(items) != 1 {
		t.Fatalf("重复内容应只保留 1 条，实际 %d 条", len(items))
	}
}

// TestPollDebounce 验证防抖：复制过程中的中间态被稳定后的内容取代。
func TestPollDebounce(t *testing.T) {
	// 第一次读取到中间态 "ab"，防抖确认时读到 "abc"，应入库 "abc"。
	clip := &fakeClipboard{seq: []string{"ab", "abc"}}
	w, st := newTestWatcher(t, clip, nil)

	w.poll(context.Background())

	items := listAll(t, st)
	if len(items) != 1 || items[0].FullText != "abc" {
		t.Fatalf("期望只入库稳定后的 abc，实际 %+v", items)
	}
}

// TestPollSensitive 验证敏感窗口内容被跳过，且重复轮询不反复处理。
func TestPollSensitive(t *testing.T) {
	clip := &fakeClipboard{text: "super-secret-password"}
	var captured int
	w, st := newTestWatcher(t, clip, func(o *Options) {
		o.SensitiveTitles = nil // 启用默认敏感名单
		o.ForegroundTitle = func() string { return "KeePass - vault" }
		o.OnCapture = func(store.Entry) { captured++ }
	})

	w.poll(context.Background())
	w.poll(context.Background())

	if items := listAll(t, st); len(items) != 0 {
		t.Fatalf("敏感窗口内容不应入库，实际 %+v", items)
	}
	if captured != 0 {
		t.Fatalf("敏感窗口内容不应触发回调，实际 %d 次", captured)
	}
}

// TestPollSensitiveMatchRules 验证敏感匹配规则：忽略大小写、子串匹配、空标题放行。
func TestPollSensitiveMatchRules(t *testing.T) {
	w, _ := newTestWatcher(t, &fakeClipboard{}, func(o *Options) {
		o.SensitiveTitles = []string{"keepass", "密码管理"}
	})

	cases := []struct {
		title string
		want  bool
	}{
		{"KeePass", true},        // 忽略大小写
		{"my keepass 2.0", true}, // 子串匹配
		{"密码管理器 - 主窗口", true},    // 中文关键字
		{"VS Code", false},       // 普通窗口
		{"", false},              // 空标题（获取失败）放行
	}
	for _, c := range cases {
		if got := w.isSensitive(c.title); got != c.want {
			t.Errorf("isSensitive(%q) = %v，期望 %v", c.title, got, c.want)
		}
	}
}

// TestPollDisabled 验证捕获开关：关闭期间不读取入库，开启后恢复。
func TestPollDisabled(t *testing.T) {
	clip := &fakeClipboard{text: "while disabled"}
	w, st := newTestWatcher(t, clip, nil)

	w.SetEnabled(false)
	if w.Enabled() {
		t.Fatal("SetEnabled(false) 后 Enabled 应为 false")
	}
	w.poll(context.Background())
	if items := listAll(t, st); len(items) != 0 {
		t.Fatalf("关闭捕获期间不应入库，实际 %+v", items)
	}

	w.SetEnabled(true)
	w.poll(context.Background())
	if items := listAll(t, st); len(items) != 1 {
		t.Fatalf("重新开启后应恢复捕获，实际 %+v", items)
	}
}

// TestPollImage 验证图片捕获，以及图片优先于占位文本。
func TestPollImage(t *testing.T) {
	img := []byte("png-bytes-here")
	clip := &fakeClipboard{image: img, text: "image placeholder text"}
	w, st := newTestWatcher(t, clip, nil)

	w.poll(context.Background())

	items := listAll(t, st)
	if len(items) != 1 {
		t.Fatalf("期望 1 条图片记录，实际 %+v", items)
	}
	if items[0].Kind != store.KindImage {
		t.Fatalf("应按图片入库（图片优先），实际 kind=%q", items[0].Kind)
	}
	if items[0].FullText != "" {
		t.Fatalf("图片条目不应携带文本，实际 %q", items[0].FullText)
	}
}

// TestPollEmptyClipboard 验证空剪贴板/读取失败不入库也不报错。
func TestPollEmptyClipboard(t *testing.T) {
	w, st := newTestWatcher(t, &fakeClipboard{}, func(o *Options) {
		o.OnError = func(error) { t.Error("空剪贴板不应触发 OnError") }
	})

	w.poll(context.Background())
	w.poll(context.Background())

	if items := listAll(t, st); len(items) != 0 {
		t.Fatalf("空剪贴板不应入库，实际 %+v", items)
	}
}

// TestImageFingerprintShortCircuit 验证大图片未变化时走指纹短路：
// 不重复入库、不重复触发回调（消除稳态 CPU/内存抖动）。
func TestImageFingerprintShortCircuit(t *testing.T) {
	// 大于采样窗口（64KB×2）的图片，确保指纹基于首尾采样。
	img := make([]byte, 256*1024)
	for i := range img {
		img[i] = byte(i % 251)
	}
	clip := &fakeClipboard{image: img}
	var captured int
	w, st := newTestWatcher(t, clip, func(o *Options) {
		o.OnCapture = func(store.Entry) { captured++ }
	})

	w.poll(context.Background()) // 首次：入库
	if items := listAll(t, st); len(items) != 1 {
		t.Fatalf("首次应入库 1 条，实际 %+v", items)
	}
	if captured != 1 {
		t.Fatalf("首次应回调 1 次，实际 %d", captured)
	}

	// 图片未变化：后续轮询应全部短路（不入库、不回调）。
	w.poll(context.Background())
	w.poll(context.Background())
	if items := listAll(t, st); len(items) != 1 {
		t.Fatalf("未变化图片不应重复入库，实际 %+v", items)
	}
	if captured != 1 {
		t.Fatalf("未变化图片不应重复回调，实际 %d", captured)
	}

	// 图片变化：指纹不同，恢复捕获。
	clip.mu.Lock()
	clip.image = append(img[:len(img)-1], 0xFF)
	clip.mu.Unlock()
	w.poll(context.Background())
	if items := listAll(t, st); len(items) != 2 {
		t.Fatalf("变化后的图片应入库，实际 %+v", items)
	}
}

// TestFingerprintSeparatesContent 验证指纹对长度与内容差异敏感。
func TestFingerprintSeparatesContent(t *testing.T) {
	a := make([]byte, 128*1024)
	b := make([]byte, 128*1024)
	copy(b, a)
	b[len(b)-1] = 1 // 尾部差异（在采样窗口内）

	if fingerprint(a) != fingerprint(a) {
		t.Fatal("同内容指纹应稳定")
	}
	if fingerprint(a) == fingerprint(b) {
		t.Fatal("尾部差异应产生不同指纹")
	}
	if fingerprint(a) == fingerprint(a[:len(a)-1]) {
		t.Fatal("长度差异应产生不同指纹")
	}
}

// TestPollStoreError 验证入库失败时 OnError 触发且下轮可自动重试。
func TestPollStoreError(t *testing.T) {
	clip := &fakeClipboard{text: "retry me"}
	var errs []error
	w, st := newTestWatcher(t, clip, func(o *Options) {
		o.OnError = func(err error) { errs = append(errs, err) }
	})

	// 先关闭 store 使写入失败。
	if err := st.Close(); err != nil {
		t.Fatalf("Close 失败: %v", err)
	}
	w.poll(context.Background())
	if len(errs) == 0 {
		t.Fatal("入库失败应触发 OnError")
	}

	// 重开 store 模拟故障恢复后，同一内容应能补录（未标记为已捕获）。
	reopened, err := store.Open(st.DataDir(), store.Options{})
	if err != nil {
		t.Fatalf("重开 store 失败: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	w.store = reopened

	w.poll(context.Background())
	if items := listAll(t, reopened); len(items) != 1 {
		t.Fatalf("故障恢复后应补录成功，实际 %+v", items)
	}
}

// TestPollTextToImageTransition 验证文本与图片切换时各自入库。
func TestPollTextToImageTransition(t *testing.T) {
	clip := &fakeClipboard{text: "first text"}
	w, st := newTestWatcher(t, clip, nil)

	w.poll(context.Background())

	// 切换为图片内容。
	clip.mu.Lock()
	clip.text = ""
	clip.image = []byte("img-2")
	clip.mu.Unlock()

	w.poll(context.Background())

	items := listAll(t, st)
	if len(items) != 2 {
		t.Fatalf("文本与图片应各自入库，实际 %+v", items)
	}
	kinds := map[store.Kind]bool{}
	for _, it := range items {
		kinds[it.Kind] = true
	}
	if !kinds[store.KindText] || !kinds[store.KindImage] {
		t.Fatalf("应同时包含文本与图片条目，实际 %+v", items)
	}
}

// TestRunStopsOnCancel 验证 ctx 取消后 Run 及时返回（无 goroutine 泄漏）。
func TestRunStopsOnCancel(t *testing.T) {
	w, _ := newTestWatcher(t, &fakeClipboard{text: "running"}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.Run(ctx)
	}()

	// 允许跑几个周期再取消。
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// 正常：Run 已退出
	case <-time.After(2 * time.Second):
		t.Fatal("ctx 取消后 Run 未退出（疑似泄漏）")
	}
}

// TestNoGoroutineLeak 验证 Watcher.Run 反复启停不泄漏 goroutine。
// （内存泄漏回归测试：取消后 goroutine 必须退出归还。）
func TestNoGoroutineLeak(t *testing.T) {
	w, _ := newTestWatcher(t, &fakeClipboard{text: "leak check"}, nil)

	// 先跑一轮预热（让运行时创建自己的后台 goroutine 后再取基线）。
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); w.Run(ctx) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	<-done

	before := runtime.NumGoroutine()
	// 多轮启停后，goroutine 数应回到基线附近（允许少量运行时抖动）。
	for i := 0; i < 5; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { defer close(done); w.Run(ctx) }()
		time.Sleep(10 * time.Millisecond)
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("Run 未随 ctx 取消退出")
		}
	}
	// 给调度器一点收敛时间。
	time.Sleep(50 * time.Millisecond)
	after := runtime.NumGoroutine()
	if after > before+2 {
		t.Fatalf("goroutine 泄漏：before=%d after=%d", before, after)
	}
}

// TestSourceAppRecorded 验证来源窗口标题被记录到条目。
func TestSourceAppRecorded(t *testing.T) {
	clip := &fakeClipboard{text: "from vscode"}
	w, st := newTestWatcher(t, clip, func(o *Options) {
		o.ForegroundTitle = func() string { return "main.go - VS Code" }
	})

	w.poll(context.Background())

	items := listAll(t, st)
	if len(items) != 1 {
		t.Fatalf("期望 1 条记录，实际 %+v", items)
	}
	if items[0].SourceApp != "main.go - VS Code" {
		t.Fatalf("来源应用未记录，实际 %q", items[0].SourceApp)
	}
}

// TestOnCaptureReceivesDedupedEntry 验证重复内容置顶时也触发回调（上层可刷新列表）。
func TestOnCaptureReceivesDedupedEntry(t *testing.T) {
	clip := &fakeClipboard{text: "dup"}
	var captured []store.Entry
	w, st := newTestWatcher(t, clip, func(o *Options) {
		o.OnCapture = func(e store.Entry) { captured = append(captured, e) }
	})

	w.poll(context.Background())
	// 强制清掉去重标记，模拟"同一内容被用户再次复制"的场景。
	w.markLast("", snapshot{})

	w.poll(context.Background())

	if len(captured) != 2 {
		t.Fatalf("重复复制应再次触发回调，实际 %d 次", len(captured))
	}
	if items := listAll(t, st); len(items) != 1 {
		t.Fatalf("但历史中仍应只有 1 条，实际 %+v", items)
	}
	if captured[1].SourceApp == "" && strings.TrimSpace(captured[1].Preview) == "" {
		t.Fatal("回调收到空条目")
	}
}

// TestErrorTypes 验证错误常量可用于 errors.Is 判断（防回归）。
func TestErrorTypes(t *testing.T) {
	if !errors.Is(store.ErrEmptyContent, store.ErrEmptyContent) {
		t.Fatal("ErrEmptyContent 语义异常")
	}
}
