package store

import (
	"strings"
	"unicode/utf8"
)

// Kind 表示历史条目的内容类型（对应 entries.kind 列）。
type Kind string

const (
	KindText  Kind = "text"  // 纯文本
	KindLink  Kind = "link"  // 链接（以 http(s):// 或 www. 开头的单行文本）
	KindCode  Kind = "code"  // 代码片段（多行且含代码符号的启发式判定）
	KindImage Kind = "image" // 图片（二进制内容，文件落盘）
)

// Entry 是一条剪贴板历史记录，与 entries 表一一对应。
//
// 注意：ImagePath 为相对 dataDir 的路径（如 "images/ab12….img"），
// 调用方拼接绝对路径时应使用 Store.ImageAbsPath。
type Entry struct {
	ID          int64  // 主键，删除/收藏时使用
	Kind        Kind   // 内容类型
	ContentHash string // sha256(内容)，去重键
	Preview     string // 列表展示文本（已折叠空白并截断）
	FullText    string // 文本全文；图片条目为空串
	ImagePath   string // 图片相对路径；文本条目为空串
	SourceApp   string // 来源窗口标题，可能为空
	IsFavorite  bool   // 是否收藏（收藏条目不参与容量淘汰）
	CreatedAt   int64  // 首次入库时间（Unix 毫秒）
	CopiedAt    int64  // 最近一次复制时间（Unix 毫秒）
}

// 入库相关的尺寸限制（超限即拒绝或截断，见 AddText / AddImage）。
const (
	// MaxTextBytes 是单条文本入库上限，超出部分截断（保留前缀）。
	MaxTextBytes = 256 * 1024
	// MaxImageBytes 是单张图片入库上限，超出直接拒绝（返回 ErrTooLarge）。
	MaxImageBytes = 8 * 1024 * 1024
	// maxPreviewRunes 是列表预览文本的最大字符数。
	maxPreviewRunes = 200
)

// DetectKind 根据文本内容推断分类（纯函数，无副作用，便于测试）。
//
// 判定规则（启发式，误判可接受，后续可在设置中手动改分类）：
//  1. 单行且以 http://、https://、www. 开头 → 链接；
//  2. 多行且包含 "{};=<>" 等代码符号 → 代码；
//  3. 其余 → 纯文本。
func DetectKind(text string) Kind {
	t := strings.TrimSpace(text)
	if t == "" {
		return KindText // 边界：空串/纯空白按纯文本处理
	}
	// 链接判定：仅单行文本（多行内容即使含 URL 也按正文处理）。
	if !strings.ContainsAny(t, "\n\r") {
		lower := strings.ToLower(t)
		if strings.HasPrefix(lower, "http://") ||
			strings.HasPrefix(lower, "https://") ||
			strings.HasPrefix(lower, "www.") {
			return KindLink
		}
	}
	// 代码判定：单行代码会被归为纯文本（无法与普通符号文本区分，可接受）。
	if strings.Contains(t, "\n") && strings.ContainsAny(t, "{};=<>") {
		return KindCode
	}
	return KindText
}

// makePreview 生成列表展示文本：折叠所有连续空白为单个空格，
// 并截断到 maxPreviewRunes 个字符（超出以 "…" 结尾）。
func makePreview(text string) string {
	// Fields 会按空白切分并丢弃空段，Join 用单空格还原，顺带去掉换行/制表符。
	runes := []rune(strings.Join(strings.Fields(text), " "))
	if len(runes) > maxPreviewRunes {
		return string(runes[:maxPreviewRunes]) + "…"
	}
	return string(runes)
}

// truncateText 将文本截断到 limit 字节，并修正可能被切断的 UTF-8 尾部序列。
//
// 返回值 truncated 表示确实发生了截断，便于调用方在预览中标注"（已截断）"。
func truncateText(text string, limit int) (out string, truncated bool) {
	if len(text) <= limit {
		return text, false
	}
	// 字节级截断可能把一个多字节字符切成两半，需从尾部回退到合法边界。
	return trimToValidUTF8([]byte(text[:limit])), true
}

// trimToValidUTF8 仅修正"截断导致的末尾不完整 UTF-8 序列"（最多回退 3 个字节）；
// 内容本身夹带的非法字节不做清洗——数据库按字节保存，展示层自行兜底。
func trimToValidUTF8(b []byte) string {
	for i := 0; i < utf8.UTFMax && len(b) > 0; i++ {
		if utf8.Valid(b) {
			break
		}
		b = b[:len(b)-1]
	}
	return string(b)
}
