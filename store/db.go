// Package store 负责剪贴板历史的持久化存储。
//
// 设计要点（见 docs/DESIGN.md §3）：
//   - 元数据（条目内容、收藏标记、设置项）存 SQLite（modernc.org/sqlite，纯 Go 无 CGO）；
//   - 图片二进制存 dataDir/images/ 下的独立文件，数据库只存相对路径，避免库文件膨胀；
//   - 所有公开方法可被多个 goroutine 并发调用（内部用互斥锁串行化写操作）；
//   - 数据库损坏时自动备份并重建，保证应用总能启动（宁可丢历史，不可崩溃）。
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // 注册纯 Go SQLite 驱动（无需 CGO，便于交叉编译与无 C 环境构建）
)

const (
	// schemaVersion 是当前表结构版本，未来升级表结构时在 migrate 中追加迁移分支。
	schemaVersion = 1

	// dbFileName 是元数据数据库文件名（位于 dataDir 下）。
	dbFileName = "history.db"

	// imagesDirName 是图片文件的存放子目录名。
	imagesDirName = "images"
)

// Open 打开（必要时创建）dataDir 下的数据库并完成表结构迁移。
//
// 容错策略：若已有数据库文件损坏（integrity_check 不通过），
// 将其重命名为 history.db.corrupt-<时间戳> 保留现场后重建空库。
//
// 参数：
//   - dataDir：数据目录（不存在则创建）；为空视为编程错误，返回错误；
//   - opts：可选配置，零值使用默认项（见 Options）。
func Open(dataDir string, opts Options) (*Store, error) {
	// 边界：dataDir 为空时无法确定存放位置，直接报错交由调用方处理。
	if strings.TrimSpace(dataDir) == "" {
		return nil, errors.New("store: dataDir 不能为空")
	}
	// MkdirAll 对已存在的目录幂等；权限失败（如磁盘满、无权限）需向上透出。
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("store: 创建数据目录失败: %w", err)
	}

	dbPath := filepath.Join(dataDir, dbFileName)
	db, err := openDB(dbPath)
	if err != nil {
		return nil, err
	}

	// 完整性检查：损坏则隔离备份并重建。
	ok, err := checkIntegrity(db)
	if err != nil {
		_ = db.Close() // 关闭失败不影响主错误语义
		return nil, err
	}
	if !ok {
		_ = db.Close()
		if err := quarantineDB(dbPath); err != nil {
			return nil, err
		}
		// 重建：此时 dbPath 已不存在，openDB 会创建全新空文件。
		if db, err = openDB(dbPath); err != nil {
			return nil, err
		}
	}

	s := &Store{db: db, dataDir: dataDir, maxEntries: opts.maxEntriesOrDefault()}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}

	// 提前创建图片目录，避免首次写图片时才暴露文件系统错误。
	if err := os.MkdirAll(filepath.Join(dataDir, imagesDirName), 0o755); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: 创建图片目录失败: %w", err)
	}
	return s, nil
}

// Close 释放数据库连接。可安全重复调用（幂等）。
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil // 边界：重复 Close 不报错
	}
	err := s.db.Close()
	s.db = nil // 置空后其他方法会返回 ErrClosed，避免操作已关闭连接
	return err
}

// openDB 打开数据库并应用运行期 PRAGMA。
func openDB(dbPath string) (*sql.DB, error) {
	// DSN 参数说明：
	//   _pragma=busy_timeout(5000) — 写冲突时最多等待 5 秒，而不是立刻报 SQLITE_BUSY；
	//   _pragma=journal_mode(WAL)  — 提升并发读写性能，进程崩溃后可自动恢复。
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)", filepath.ToSlash(dbPath))
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: 打开数据库失败: %w", err)
	}
	// SQLite 是单写者模型：限制为单连接可彻底避免并发写导致的 "database is locked"，
	// 对剪贴板这种低频写入场景没有性能损失。
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0) // 连接长期复用，避免反复打开/关闭文件
	return db, nil
}

// checkIntegrity 执行 PRAGMA integrity_check，返回数据库是否完好。
func checkIntegrity(db *sql.DB) (bool, error) {
	var result string
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&result); err != nil {
		// 扫描失败说明文件根本不是合法 SQLite 文件（例如被其他程序写坏）。
		return false, nil //nolint:nilerr // 该情况按"损坏"处理，走备份重建流程
	}
	return strings.EqualFold(strings.TrimSpace(result), "ok"), nil
}

// quarantineDB 将损坏的数据库（及其 WAL/SHM 辅助文件）移出场外，保留现场供日后排查。
func quarantineDB(dbPath string) error {
	stamp := time.Now().Format("20060102-150405")
	backup := fmt.Sprintf("%s.corrupt-%s", dbPath, stamp)
	// 文件不存在时无需备份（例如用户手动删除了库文件）。
	if err := os.Rename(dbPath, backup); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("store: 备份损坏数据库失败: %w", err)
	}
	// WAL/SHM 辅助文件必须一并移除，否则新库可能读到旧日志导致再次损坏。
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Remove(dbPath + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("store: 清理数据库辅助文件失败: %w", err)
		}
	}
	return nil
}

// migrate 创建/升级表结构，并记录 schema 版本。
func (s *Store) migrate() error {
	// 建表语句均带 IF NOT EXISTS，重复执行幂等。
	stmts := []string{
		// meta：存放 schema 版本等元信息
		`CREATE TABLE IF NOT EXISTS meta (
			key   TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
		// entries：剪贴板历史主表
		`CREATE TABLE IF NOT EXISTS entries (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			kind         TEXT    NOT NULL,            -- 'text' | 'link' | 'code' | 'image'
			content_hash TEXT    NOT NULL UNIQUE,     -- sha256(内容)，去重键：重复复制→置顶而非新增
			preview      TEXT    NOT NULL,            -- 列表展示用截断文本
			full_text    TEXT,                        -- 文本全文；图片条目为 NULL
			image_path   TEXT,                        -- 图片相对路径；文本条目为 NULL
			source_app   TEXT,                        -- 来源窗口标题（可为空）
			is_favorite  INTEGER NOT NULL DEFAULT 0,  -- 收藏标记：不参与容量淘汰
			created_at   INTEGER NOT NULL,            -- 首次入库时间（Unix 毫秒）
			copied_at    INTEGER NOT NULL             -- 最近一次复制时间（去重时刷新）
		)`,
		// 列表排序走该索引：收藏优先 + 时间倒序
		`CREATE INDEX IF NOT EXISTS idx_entries_copied ON entries(is_favorite, copied_at DESC)`,
		// settings：键值对配置（快捷键、历史上限、轮询间隔等）
		`CREATE TABLE IF NOT EXISTS settings (
			key   TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("store: 建表失败: %w", err)
		}
	}
	// 记录当前版本；未来按 version 分支做增量迁移（如 ALTER TABLE）。
	if _, err := s.db.Exec(
		`INSERT INTO meta(key, value) VALUES('schema_version', ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		fmt.Sprintf("%d", schemaVersion),
	); err != nil {
		return fmt.Errorf("store: 记录 schema 版本失败: %w", err)
	}
	return nil
}
