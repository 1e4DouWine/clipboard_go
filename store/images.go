package store

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	_ "image/gif"  // 注册 GIF 解码器（读取图片尺寸用）
	_ "image/jpeg" // 注册 JPEG 解码器
	_ "image/png"  // 注册 PNG 解码器
	"log"
	"os"
	"path/filepath"
	"time"
)

// imageExt 是图片文件的统一后缀。
// mygo 的 Clipboard.ReadImage 未承诺具体编码格式，因此不按格式分后缀，
// 以内容哈希命名 + 统一后缀，读取时用 image.Decode 自动识别。
const imageExt = ".img"

// AddImage 写入一条图片历史：二进制落盘 + 元数据入库。
//
// 边界处理：
//   - 0 字节数据 → ErrEmptyContent；
//   - 超过 MaxImageBytes → ErrTooLarge（保护内存与磁盘）；
//   - 相同图片（sha256 相同）重复复制 → 与文本一致走"置顶不新增"；
//   - 图片尺寸无法解析时预览降级为"图片"，不影响入库；
//   - 文件写入采用"临时文件 + rename"，保证磁盘上不会出现半个文件。
func (s *Store) AddImage(data []byte, sourceApp string) (Entry, error) {
	if len(data) == 0 {
		return Entry{}, ErrEmptyContent
	}
	if len(data) > MaxImageBytes {
		return Entry{}, ErrTooLarge
	}

	hash := hashBytes(data)
	relPath := filepath.ToSlash(filepath.Join(imagesDirName, hash+imageExt))
	preview := imagePreview(data)
	now := time.Now().UnixMilli()

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return Entry{}, ErrClosed
	}

	// 先落盘（幂等：同名文件已存在即跳过），再写元数据，
	// 保证任何时刻数据库里的 image_path 都指向存在的文件。
	if err := s.writeImageFileLocked(relPath, data); err != nil {
		return Entry{}, err
	}

	if _, err := s.db.Exec(`
		INSERT INTO entries (kind, content_hash, preview, full_text, image_path, source_app, is_favorite, created_at, copied_at)
		VALUES (?, ?, ?, NULL, ?, ?, 0, ?, ?)
		ON CONFLICT(content_hash) DO UPDATE SET
			copied_at  = excluded.copied_at,
			source_app = excluded.source_app`,
		string(KindImage), hash, preview, relPath, sourceApp, now, now,
	); err != nil {
		return Entry{}, fmt.Errorf("store: 写入图片历史失败: %w", err)
	}

	e, err := s.getEntryByHashLocked(hash)
	if err != nil {
		return Entry{}, err
	}
	if err := s.purgeLocked(); err != nil {
		log.Printf("store: 容量淘汰失败（已忽略）: %v", err)
	}
	return e, nil
}

// ImageAbsPath 把条目中的图片相对路径解析为绝对路径。
// 非图片条目（ImagePath 为空）返回空串，调用方据此判断是否需要加载图片。
func (s *Store) ImageAbsPath(relPath string) string {
	if relPath == "" {
		return ""
	}
	// 防御路径穿越：即使数据库被外部篡改，也不会读写数据目录之外的文件。
	clean := filepath.Clean(filepath.FromSlash(relPath))
	if filepath.IsAbs(clean) || clean == ".." || len(clean) >= 2 && clean[:2] == ".." {
		return ""
	}
	return filepath.Join(s.dataDir, clean)
}

// ReadImage 读取图片条目的二进制内容（快速粘贴/回写剪贴板时使用）。
func (s *Store) ReadImage(e Entry) ([]byte, error) {
	if e.Kind != KindImage || e.ImagePath == "" {
		return nil, errors.New("store: 该条目不是图片")
	}
	abs := s.ImageAbsPath(e.ImagePath)
	if abs == "" {
		return nil, errors.New("store: 图片路径非法")
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		// 文件被外部删除属于可预期故障，包装后交由 UI 层提示并允许删除该条目。
		return nil, fmt.Errorf("store: 读取图片文件失败: %w", err)
	}
	return data, nil
}

// writeImageFileLocked 以"临时文件 + 原子重命名"写入图片，避免半成品文件。
// 目标已存在（同内容重复复制）时直接跳过，减少磁盘写入。
func (s *Store) writeImageFileLocked(relPath string, data []byte) error {
	abs := s.ImageAbsPath(relPath)
	if abs == "" {
		return errors.New("store: 图片路径非法")
	}
	// 幂等短路：同哈希文件必然内容一致，无需重写。
	if _, err := os.Stat(abs); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("store: 检查图片文件失败: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return fmt.Errorf("store: 创建图片目录失败: %w", err)
	}
	tmp := abs + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("store: 写入图片临时文件失败: %w", err)
	}
	// rename 在同一目录内是原子操作；Windows 上目标已存在时 os.Rename 会替换。
	if err := os.Rename(tmp, abs); err != nil {
		_ = os.Remove(tmp) // 清理残留临时文件，失败可忽略
		return fmt.Errorf("store: 提交图片文件失败: %w", err)
	}
	return nil
}

// removeImageFile 删除图片文件；不存在视为成功（幂等）。
func (s *Store) removeImageFile(relPath string) error {
	abs := s.ImageAbsPath(relPath)
	if abs == "" {
		return nil // 路径非法时无需删除任何东西
	}
	if err := os.Remove(abs); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("删除图片文件失败: %w", err)
	}
	return nil
}

// imagePreview 解析图片尺寸生成预览文本（如 "图片 1280×720"）。
// 解码失败（不支持的格式/损坏数据）时降级为 "图片"，不阻塞入库。
func imagePreview(data []byte) string {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return "图片"
	}
	return fmt.Sprintf("图片 %d×%d", cfg.Width, cfg.Height)
}
