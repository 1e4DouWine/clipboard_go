// Package watch 负责后台剪贴板监听：轮询检测新内容，完成去重、防抖、
// 敏感窗口过滤后写入 store，并通过回调通知上层（UI 更新等）。
//
// 线程模型（遵守 mygo 约定）：
//   - Watcher.Run 在后台 goroutine 阻塞运行，绝不触碰 UI 对象；
//   - OnCapture 回调在监听 goroutine 上触发，上层如需更新 UI，
//     必须自行用 mygo 的 Window.Update 投递到主线程。
package watch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"my-go-app/store"
)

// Clipboard 是 watch 对剪贴板能力的最小依赖。
//
// 抽象成接口的目的：
//  1. 测试可注入 fake，不依赖 mygo 应用环境（mygo.Clipboard 需要 App ready）；
//  2. 与具体 UI 框架解耦，后续可替换实现（如直接调 Win32）。
//
// 实现约定：读取失败（如剪贴板被其他进程占用）时返回空值，
// watch 会视为"本轮无内容"并在下轮重试，不视为错误。
type Clipboard interface {
	// ReadText 读取剪贴板纯文本；无文本或读取失败返回 ""。
	ReadText() string
	// ReadImage 读取剪贴板图片字节；无图片或读取失败返回 nil。
	ReadImage() []byte
}

// Options 是 New 的配置项。
type Options struct {
	// Clipboard 为剪贴板数据源，必填。
	Clipboard Clipboard
	// Store 为历史存储，必填。
	Store *store.Store
	// OnCapture 在捕获到新条目后触发（监听 goroutine 上调用，禁止直接操作 UI）。
	// 可为 nil。注意：相同内容重复复制走"置顶"语义时也会触发，回调拿到的是最新条目。
	OnCapture func(store.Entry)
	// OnError 在写入存储失败时触发，可为 nil。失败后该内容不标记为已捕获，
	// 下个轮询周期自动重试，不会丢数据。
	OnError func(error)
	// Interval 为轮询间隔。<=0 取默认 500ms；下限钳制为 20ms，防止误配成忙轮询。
	Interval time.Duration
	// Settle 为防抖确认窗口：检测到内容变化后，等待该时间再读一次，
	// 以稳定后的内容为准入库，避免把复制过程中的中间态写入历史。
	// <=0 取默认 120ms。
	Settle time.Duration
	// SensitiveTitles 为敏感窗口标题关键字（不区分大小写、子串匹配），
	// 命中则跳过捕获（保护密码管理器等场景）。nil 取默认名单；空切片表示不过滤。
	SensitiveTitles []string
	// ForegroundTitle 返回当前前台窗口标题，用于敏感过滤与来源记录。
	// nil 取平台实现（Windows 调 Win32；其他平台返回空）。
	// 仅用于测试注入，生产代码一般无需设置。
	ForegroundTitle func() string
}

// 默认参数常量。
const (
	// DefaultInterval 是默认轮询间隔（与原型一致，CPU 占用可忽略）。
	DefaultInterval = 500 * time.Millisecond
	// minInterval 是轮询间隔下限，防止把间隔误配为 0 导致忙轮询。
	minInterval = 20 * time.Millisecond
	// DefaultSettle 是默认防抖确认窗口。
	DefaultSettle = 120 * time.Millisecond
)

// DefaultSensitiveTitles 是默认的敏感窗口标题关键字（子串匹配、忽略大小写）。
// 覆盖常见密码管理器；用户可在后续设置面板中覆盖此名单。
var DefaultSensitiveTitles = []string{
	"keepass", "1password", "bitwarden", "lastpass", "dashlane", "enpass",
	"密码管理", "password manager",
}

// Watcher 是后台剪贴板监听器。零值不可用，请通过 New 创建。
// 所有方法并发安全：Run 在后台 goroutine 运行，SetEnabled 可随时从任意 goroutine 调用。
type Watcher struct {
	clip      Clipboard
	store     *store.Store
	onCapture func(store.Entry)
	onError   func(error)
	interval  time.Duration
	settle    time.Duration
	sensitive []string
	foregnd   func() string

	enabled  atomic.Bool // 捕获开关（可临时暂停捕获，如复制密码时）
	lastMu   sync.Mutex  // 保护 lastHash/lastFP（poll 与潜在的多实例调用）
	lastHash string      // 上次入库内容的哈希，用于去重
	lastFP   string      // 上次图片指纹，用于大图片未变化时短路（见 fingerprint）
}

// New 校验并归一化配置，创建监听器。
func New(opts Options) (*Watcher, error) {
	// 边界：依赖缺失属于编程错误，启动期即失败，避免运行期空指针。
	if opts.Clipboard == nil {
		return nil, errors.New("watch: Clipboard 不能为空")
	}
	if opts.Store == nil {
		return nil, errors.New("watch: Store 不能为空")
	}

	interval := opts.Interval
	if interval <= 0 {
		interval = DefaultInterval
	}
	if interval < minInterval {
		interval = minInterval // 钳制下限，防止忙轮询
	}
	settle := opts.Settle
	if settle <= 0 {
		settle = DefaultSettle
	}

	sensitive := opts.SensitiveTitles
	if sensitive == nil {
		sensitive = DefaultSensitiveTitles // nil → 默认名单；显式空切片 → 不过滤
	}
	// 归一化为小写，匹配时无需反复小写化。
	norm := make([]string, 0, len(sensitive))
	for _, s := range sensitive {
		if s = strings.ToLower(strings.TrimSpace(s)); s != "" {
			norm = append(norm, s)
		}
	}

	foregnd := opts.ForegroundTitle
	if foregnd == nil {
		foregnd = foregroundTitle // 平台实现（见 foreground_*.go）
	}

	w := &Watcher{
		clip:      opts.Clipboard,
		store:     opts.Store,
		onCapture: opts.OnCapture,
		onError:   opts.OnError,
		interval:  interval,
		settle:    settle,
		sensitive: norm,
		foregnd:   foregnd,
	}
	w.enabled.Store(true) // 默认开启捕获
	return w, nil
}

// SetEnabled 开/关捕获。关闭期间轮询继续但不入库，
// 适合"临时复制敏感内容"场景；重新开启后新内容恢复正常捕获。
func (w *Watcher) SetEnabled(on bool) { w.enabled.Store(on) }

// Enabled 返回当前是否处于捕获状态。
func (w *Watcher) Enabled() bool { return w.enabled.Load() }

// Run 启动轮询循环，阻塞运行直到 ctx 取消。
//
// 退出保证：ctx 取消后 Run 会在当前周期（含防抖等待）结束后尽快返回，
// 不会再触发 OnCapture/OnError 回调，调用方可安全释放 UI 资源。
func (w *Watcher) Run(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop() // 释放计时器资源，防止泄漏

	for {
		select {
		case <-ctx.Done():
			return // 正常退出路径：应用退出/窗口关闭时由上层取消
		case <-ticker.C:
			w.poll(ctx)
		}
	}
}

// poll 执行一次检测周期：读取 → 去重 → 防抖确认 → 敏感过滤 → 入库。
func (w *Watcher) poll(ctx context.Context) {
	// 捕获开关：关闭期间连剪贴板都不读，进一步降低打扰。
	if !w.enabled.Load() {
		return
	}

	snap, hash, unchanged := w.readSnapshot()
	if unchanged {
		return // 大图片指纹未变化：跳过全量哈希/确认/入库（稳态零开销）
	}
	if hash == "" {
		return // 剪贴板为空或读取失败（被占用）：静默跳过，下轮重试
	}
	if w.sameAsLast(hash) {
		return // 去重：与上次入库内容一致
	}

	// 防抖确认：稍等片刻再读一次，以稳定后的内容为准。
	// 场景：连续复制/富文本复制可能让剪贴板处于短暂中间态。
	// 等待期间 ctx 取消则放弃本轮（应用正在退出，不再入库）。
	select {
	case <-ctx.Done():
		return
	case <-time.After(w.settle):
	}
	if snap2, hash2, unchanged2 := w.readSnapshot(); hash2 != "" && !unchanged2 {
		snap, hash = snap2, hash2
	}
	if w.sameAsLast(hash) {
		return // 确认后内容回落到上次值，无需处理
	}

	// 敏感过滤：命中黑名单窗口则跳过，并记录哈希避免反复检查。
	// 同一前台窗口标题也作为来源信息入库（可能为空字符串）。
	title := strings.TrimSpace(w.foregnd())
	if w.isSensitive(title) {
		w.markLast(hash, snap)
		return
	}

	entry, err := w.capture(snap, title)
	if err != nil {
		// 入库失败（磁盘满、DB 故障等）：报告但不标记已捕获，
		// 下个周期自动重试，避免丢历史。
		if w.onError != nil {
			w.onError(err)
		}
		return
	}
	w.markLast(hash, snap)

	if w.onCapture != nil {
		w.onCapture(entry) // 回调在监听 goroutine 上，上层须自行转主线程
	}
}

// snapshot 是一次剪贴板读取的结果：文本与图片二选一（图片优先）。
type snapshot struct {
	text  string
	image []byte
}

// fingerprint 是内容的廉价指纹（长度 + 首尾采样哈希），用于"未变化"快速判断。
//
// 内存/CPU 优化背景：剪贴板常驻一张大图片时，每次轮询都对整个图片做
// sha256（8MB ≈ 数毫秒 CPU）会产生持续的分配与 GC 压力。指纹只哈希
// 首尾各 64KB：指纹不同才计算全量哈希与入库；指纹相同直接短路。
// 理论上存在"首尾相同、中间不同"的漏判（概率极低），后果只是该次
// 变化延迟到下一轮指纹变化时被捕获，不影响最终一致性。
func fingerprint(data []byte) string {
	const sample = 64 * 1024
	h := sha256.New()
	// 长度先行混入：长度不同的内容必然指纹不同。
	fmt.Fprintf(h, "%d:", len(data))
	head := data
	if len(head) > sample {
		head = head[:sample]
	}
	h.Write(head)
	if len(data) > sample {
		tail := data[len(data)-sample:]
		h.Write(tail)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// readSnapshot 读取当前剪贴板内容并返回内容哈希。
//
// 优先级：图片非空按图片处理（部分软件复制图片时同时提供占位文本，
// 以图片为准更符合用户直觉）；两者都空返回空哈希表示"无内容"。
//
// 返回值 unchanged 表示图片指纹与上次一致（内容大概率未变化），
// 调用方可跳过全量哈希、确认重读与入库流程。
func (w *Watcher) readSnapshot() (snap snapshot, hash string, unchanged bool) {
	// 图片读取失败返回 nil，与"没有图片"无法区分，均按无图片处理（下轮重试）。
	img := w.clip.ReadImage()
	if len(img) > 0 {
		// 指纹短路：大图片未变化时避免全量 sha256（内存/CPU 抖动的主要来源）。
		fp := fingerprint(img)
		w.lastMu.Lock()
		same := fp == w.lastFP && w.lastFP != ""
		w.lastMu.Unlock()
		if same {
			return snapshot{}, "", true
		}
		return snapshot{image: img}, hashBytes(img), false
	}
	text := w.clip.ReadText()
	if strings.TrimSpace(text) == "" {
		return snapshot{}, "", false // 空文本不入库（store 也会拒绝，这里提前短路）
	}
	return snapshot{text: text}, hashBytes([]byte(text)), false
}

// capture 将稳定后的内容写入 store；图片与文本走不同入库路径。
func (w *Watcher) capture(snap snapshot, sourceApp string) (store.Entry, error) {
	if len(snap.image) > 0 {
		return w.store.AddImage(snap.image, sourceApp)
	}
	return w.store.AddText(snap.text, sourceApp)
}

// sameAsLast 判断内容哈希是否与上次入库一致（去重用）。
func (w *Watcher) sameAsLast(hash string) bool {
	w.lastMu.Lock()
	defer w.lastMu.Unlock()
	return hash != "" && hash == w.lastHash
}

// markLast 记录已处理（入库或有意跳过）的内容哈希；图片内容同时记录指纹。
func (w *Watcher) markLast(hash string, snap snapshot) {
	w.lastMu.Lock()
	defer w.lastMu.Unlock()
	w.lastHash = hash
	if len(snap.image) > 0 {
		w.lastFP = fingerprint(snap.image) // 记录指纹供下轮短路
	} else {
		w.lastFP = "" // 文本内容清空图片指纹，避免误短路
	}
}

// isSensitive 判断窗口标题是否命中敏感名单（忽略大小写的子串匹配）。
// 空名单/空标题均不命中：无法判断时不拦截，避免误伤正常捕获。
func (w *Watcher) isSensitive(title string) bool {
	if title == "" || len(w.sensitive) == 0 {
		return false
	}
	lower := strings.ToLower(title)
	for _, keyword := range w.sensitive {
		if strings.Contains(lower, keyword) {
			return true
		}
	}
	return false
}

// hashBytes 计算内容的 sha256 十六进制摘要（与 store 的去重键算法一致）。
func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
