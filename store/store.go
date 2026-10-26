package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

// 预定义错误，调用方用 errors.Is 判断，便于 UI 层给出针对性提示。
var (
	// ErrEmptyContent 表示内容为空（空白文本或 0 字节图片），拒绝入库。
	ErrEmptyContent = errors.New("store: 内容为空")
	// ErrTooLarge 表示内容超过单条上限（图片 > 8MB）。
	ErrTooLarge = errors.New("store: 内容超过单条上限")
	// ErrNotFound 表示按 ID 未找到条目（可能已被淘汰或删除）。
	ErrNotFound = errors.New("store: 条目不存在")
)

// DefaultMaxEntries 是默认历史条目上限（含收藏）。
const DefaultMaxEntries = 10000

// Filter 描述一次列表查询的过滤条件（各项皆可选，零值 = 不过滤）。
type Filter struct {
	// Text 为关键字；空白表示不过滤（LIKE 转义由 store 内部完成）。
	Text string
	// Kind 为内容类型；空串表示不限类型。
	Kind Kind
	// FavoritesOnly 为真时只返回收藏条目。
	FavoritesOnly bool
}

// Options 是 Open 的可选配置项。
type Options struct {
	// MaxEntries 为历史条目总数上限（含收藏条目）；<=0 时取 DefaultMaxEntries。
	// 超出上限时按 copied_at 淘汰最旧的"非收藏"条目。
	MaxEntries int
}

// maxEntriesOrDefault 归一化配置，防御调用方传入非法值。
func (o Options) maxEntriesOrDefault() int {
	if o.MaxEntries <= 0 {
		return DefaultMaxEntries
	}
	return o.MaxEntries
}

// Store 是历史持久化的入口。所有方法并发安全（内部串行化）。
type Store struct {
	mu         sync.Mutex // 串行化所有数据库访问，避免 SQLITE_BUSY 与读改写竞态
	db         *sql.DB
	dataDir    string // 数据目录绝对/相对路径，图片相对路径的基准
	maxEntries int    // 条目上限
}

// DataDir 返回数据目录路径（图片绝对路径拼接用）。
func (s *Store) DataDir() string { return s.dataDir }

// AddText 写入一条文本历史。
//
// 去重语义：若相同内容（sha256 相同）已存在，则不新增条目，
// 仅刷新 copied_at 与 source_app（即"重复复制→置顶"）。
//
// 边界处理：
//   - 空白文本 → ErrEmptyContent；
//   - 超过 MaxTextBytes → 截断入库并修正 UTF-8 尾部；
//   - 数据库写失败 → 包装错误返回；容量淘汰失败仅记日志，不影响本次写入。
func (s *Store) AddText(text, sourceApp string) (Entry, error) {
	// 用 TrimSpace 判空，但保留原始内容的首尾空白（用户可能复制缩进代码）。
	if strings.TrimSpace(text) == "" {
		return Entry{}, ErrEmptyContent
	}

	full, truncated := truncateText(text, MaxTextBytes)
	hash := HashBytes([]byte(full))
	preview := makePreview(full)
	if truncated {
		preview += "（已截断）" // 明示用户内容不完整，避免误以为全文已存
	}
	kind := DetectKind(full)
	now := time.Now().UnixMilli()

	s.mu.Lock()
	defer s.mu.Unlock()

	// upsert：冲突（同 content_hash）时只刷新最近复制时间与来源应用。
	if _, err := s.db.Exec(`
		INSERT INTO entries (kind, content_hash, preview, full_text, image_path, source_app, is_favorite, created_at, copied_at)
		VALUES (?, ?, ?, ?, NULL, ?, 0, ?, ?)
		ON CONFLICT(content_hash) DO UPDATE SET
			copied_at  = excluded.copied_at,
			source_app = excluded.source_app`,
		string(kind), hash, preview, full, sourceApp, now, now,
	); err != nil {
		return Entry{}, fmt.Errorf("store: 写入文本历史失败: %w", err)
	}

	// 取回完整行：命中已有条目时会带上其 created_at / is_favorite。
	e, err := s.getEntryByHashLocked(hash)
	if err != nil {
		return Entry{}, err
	}

	// 容量淘汰失败只记录日志：入库已成功，淘汰可留待下次写入重试。
	if err := s.purgeLocked(); err != nil {
		log.Printf("store: 容量淘汰失败（已忽略）: %v", err)
	}
	return e, nil
}

// Get 按 ID 查询单条历史。
func (s *Store) Get(id int64) (Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	e, err := scanEntry(s.db.QueryRow(selectColumns+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Entry{}, ErrNotFound
	}
	return e, err
}

// List 按"收藏优先 + 最近复制"倒序返回历史（含全文），最多 limit 条。
// limit <= 0 时返回全部（UI 层应传入合理上限避免一次加载过多）。
//
// 注意：含 full_text 的完整加载仅适合小数据量场景（如测试/导出）。
// 列表 UI 请用 ListBrief 避免全文常驻内存。
func (s *Store) List(limit int) ([]Entry, error) {
	return s.listFiltered(Filter{}, limit, false)
}

// ListBrief 与 List 排序一致，但不加载 full_text（返回的 Entry.FullText 为空）。
// 全文可通过 Get(id) 按需读取——列表展示/过滤场景专用，显著降低内存峰值。
func (s *Store) ListBrief(limit int) ([]Entry, error) {
	return s.listFiltered(Filter{}, limit, true)
}

// Search 按关键字过滤历史（大小写不敏感，基于 SQLite LIKE）。
// 空/空白查询等价于 List。用户输入中的 % _ \ 会被转义为字面量，不参与模式匹配。
//
// 注意：含 full_text 的完整加载仅适合小数据量场景；列表 UI 请用 SearchBrief。
func (s *Store) Search(query string, limit int) ([]Entry, error) {
	return s.listFiltered(Filter{Text: query}, limit, false)
}

// SearchBrief 与 Search 结果集一致，但不加载 full_text（Entry.FullText 为空）。
// 搜索仍匹配 full_text 列（全文检索语义不变），只是不把全文取回内存。
func (s *Store) SearchBrief(query string, limit int) ([]Entry, error) {
	return s.listFiltered(Filter{Text: query}, limit, true)
}

// ListFiltered 按过滤条件返回历史（不含 full_text，列表专用）。
//
// 过滤在 SQL 层完成：类型/收藏/关键字一起下推到 WHERE，LIMIT 作用于
// 过滤之后的结果——因此"只看图片"不会因为"最新 500 条里恰好没有图片"
// 而漏掉更早的图片（这正是内存过滤的老问题）。
func (s *Store) ListFiltered(f Filter, limit int) ([]Entry, error) {
	return s.listFiltered(f, limit, true)
}

// listFiltered 是各查询的共用实现：brief 决定是否加载 full_text。
func (s *Store) listFiltered(f Filter, limit int, brief bool) ([]Entry, error) {
	cols := selectColumns
	if brief {
		cols = selectColumnsBrief
	}
	var where []string
	var args []any
	if f.Kind != "" {
		where = append(where, "kind = ?")
		args = append(args, string(f.Kind))
	}
	if f.FavoritesOnly {
		where = append(where, "is_favorite = 1")
	}
	if q := strings.TrimSpace(f.Text); q != "" {
		// 关键字同时匹配预览与全文；加括号以免与其他条件组合时优先级出错。
		where = append(where, `(preview LIKE ? ESCAPE '\' OR full_text LIKE ? ESCAPE '\')`)
		like := "%" + escapeLike(q) + "%"
		args = append(args, like, like)
	}

	query := cols
	if len(where) > 0 {
		query += ` WHERE ` + strings.Join(where, " AND ")
	}
	query += ` ORDER BY is_favorite DESC, copied_at DESC`
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	return s.queryEntries(query, args...)
}

// Delete 删除单条历史，并同时清理其图片文件（若为图片条目）。
func (s *Store) Delete(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 先取出图片路径再删除，避免删完后无法定位文件。
	var imagePath sql.NullString
	if err := s.db.QueryRow(`SELECT image_path FROM entries WHERE id = ?`, id).Scan(&imagePath); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("store: 查询待删除条目失败: %w", err)
	}
	if _, err := s.db.Exec(`DELETE FROM entries WHERE id = ?`, id); err != nil {
		return fmt.Errorf("store: 删除条目失败: %w", err)
	}
	// 删文件失败只记日志：条目已删，残留文件可在下次启动清理。
	if imagePath.Valid && imagePath.String != "" {
		if err := s.removeImageFile(imagePath.String); err != nil {
			log.Printf("store: 删除图片文件失败（已忽略）: %v", err)
		}
	}
	return nil
}

// ToggleFavorite 切换收藏标记，返回切换后的状态。
func (s *Store) ToggleFavorite(id int64) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var fav bool
	err := s.db.QueryRow(`UPDATE entries SET is_favorite = 1 - is_favorite WHERE id = ? RETURNING is_favorite`, id).Scan(&fav)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrNotFound
	}
	if err != nil {
		return false, fmt.Errorf("store: 切换收藏失败: %w", err)
	}
	return fav, nil
}

// Clear 清空历史。keepFavorites 为 true 时保留收藏条目。
// 同时清理不再被引用的图片文件（收藏保留时其图片不受影响）。
func (s *Store) Clear(keepFavorites bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	where := `TRUE` // 全部删除
	if keepFavorites {
		where = `is_favorite = 0`
	}
	// 收集将被删除的图片路径，删除条目后统一清理文件。
	rows, err := s.db.Query(`SELECT image_path FROM entries WHERE ` + where + ` AND image_path IS NOT NULL`)
	if err != nil {
		return fmt.Errorf("store: 查询待清理图片失败: %w", err)
	}
	var paths []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			_ = rows.Close()
			return fmt.Errorf("store: 读取图片路径失败: %w", err)
		}
		paths = append(paths, p)
	}
	err = rows.Err()
	_ = rows.Close() // 提前关闭，避免与后续写操作在单连接上互相阻塞
	if err != nil {
		return fmt.Errorf("store: 读取待清理图片失败: %w", err)
	}

	if _, err := s.db.Exec(`DELETE FROM entries WHERE ` + where); err != nil {
		return fmt.Errorf("store: 清空历史失败: %w", err)
	}
	for _, p := range paths {
		if err := s.removeImageFile(p); err != nil {
			log.Printf("store: 删除图片文件失败（已忽略）: %v", err)
		}
	}
	return nil
}

// GetSetting 读取配置项。第二个返回值表示 key 是否存在。
func (s *Store) GetSetting(key string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var v string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("store: 读取配置失败: %w", err)
	}
	return v, true, nil
}

// SetSetting 写入/更新配置项（upsert）。
func (s *Store) SetSetting(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if strings.TrimSpace(key) == "" {
		return errors.New("store: 配置 key 不能为空") // 边界：空 key 无意义，直接拒绝
	}
	if _, err := s.db.Exec(
		`INSERT INTO settings(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value,
	); err != nil {
		return fmt.Errorf("store: 写入配置失败: %w", err)
	}
	return nil
}

// ---- 内部工具（调用前必须已持有 s.mu）----

// selectColumns 是各查询共用的列清单（用 COALESCE 把 NULL 归一为空串，简化扫描）。
const selectColumns = `SELECT id, kind, content_hash, preview,
	COALESCE(full_text, ''), COALESCE(image_path, ''), COALESCE(source_app, ''),
	is_favorite, created_at, copied_at
	FROM entries`

// selectColumnsBrief 是列表/搜索专用列清单：不加载 full_text（以空串占位）。
//
// 内存优化背景：单条文本上限 256KB，若列表一次加载 500 条全文，
// 内存峰值可达百 MB 级；而列表 UI 只展示 preview（≤200 字符），
// 全文仅在"粘贴回写"时才需要——因此列表走 Brief，回写时按需 Get(id)。
const selectColumnsBrief = `SELECT id, kind, content_hash, preview,
	'', COALESCE(image_path, ''), COALESCE(source_app, ''),
	is_favorite, created_at, copied_at
	FROM entries`

// getEntryByHashLocked 按内容哈希取回条目（upsert 后回读用）。
func (s *Store) getEntryByHashLocked(hash string) (Entry, error) {
	e, err := scanEntry(s.db.QueryRow(selectColumns+` WHERE content_hash = ?`, hash))
	if err != nil {
		return Entry{}, fmt.Errorf("store: 回读条目失败: %w", err)
	}
	return e, nil
}

// queryEntries 执行查询并聚合为切片；查询失败返回包装错误。
func (s *Store) queryEntries(query string, args ...any) ([]Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: 查询历史失败: %w", err)
	}
	defer rows.Close()

	// 预分配少量容量：大多数查询会带 limit，先按 64 起步减少扩容。
	entries := make([]Entry, 0, 64)
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: 遍历查询结果失败: %w", err) // 边界：遍历中断（磁盘错误等）
	}
	return entries, nil
}

// purgeLocked 按容量上限淘汰旧条目：收藏条目永不淘汰。
// 只淘汰"非收藏"条目；若收藏本身已超上限，则保留现状（宁可超限，不删用户珍藏）。
func (s *Store) purgeLocked() error {
	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM entries`).Scan(&total); err != nil {
		return fmt.Errorf("store: 统计条目数失败: %w", err)
	}
	if total <= s.maxEntries {
		return nil
	}
	// 取出最旧的非收藏条目，数量 = 超出部分。
	rows, err := s.db.Query(
		`SELECT id, COALESCE(image_path, '') FROM entries WHERE is_favorite = 0 ORDER BY copied_at ASC LIMIT ?`,
		total-s.maxEntries,
	)
	if err != nil {
		return fmt.Errorf("store: 查询待淘汰条目失败: %w", err)
	}
	var ids []int64
	var paths []string
	for rows.Next() {
		var id int64
		var p string
		if err := rows.Scan(&id, &p); err != nil {
			_ = rows.Close()
			return fmt.Errorf("store: 读取待淘汰条目失败: %w", err)
		}
		ids = append(ids, id)
		if p != "" {
			paths = append(paths, p)
		}
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return fmt.Errorf("store: 读取待淘汰条目失败: %w", err)
	}

	for _, id := range ids {
		if _, err := s.db.Exec(`DELETE FROM entries WHERE id = ?`, id); err != nil {
			return fmt.Errorf("store: 淘汰条目失败: %w", err)
		}
	}
	// 淘汰产生的孤儿图片一并清理；失败仅记日志（不影响数据一致性）。
	for _, p := range paths {
		if err := s.removeImageFile(p); err != nil {
			log.Printf("store: 清理淘汰图片失败（已忽略）: %v", err)
		}
	}
	return nil
}

// scanEntry 从查询行扫描出一个 Entry，屏蔽 NULL 与类型转换细节。
func scanEntry(row interface{ Scan(dest ...any) error }) (Entry, error) {
	var e Entry
	var kind string
	var fav int
	err := row.Scan(&e.ID, &kind, &e.ContentHash, &e.Preview,
		&e.FullText, &e.ImagePath, &e.SourceApp, &fav, &e.CreatedAt, &e.CopiedAt)
	if err != nil {
		return Entry{}, err // sql.ErrNoRows 由调用方翻译
	}
	e.Kind = Kind(kind)
	e.IsFavorite = fav == 1
	return e, nil
}

// HashBytes 计算内容的 sha256 十六进制摘要（去重键）。
//
// 导出供 watch 包复用：监听器需要在入库前算一遍哈希来判重，
// 两处必须用同一套算法，否则去重语义会漂移。
func HashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// escapeLike 转义 LIKE 模式中的特殊字符，使用户输入按字面量匹配。
func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}
