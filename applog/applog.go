// Package applog 提供统一的应用日志与 panic 兜底能力。
//
// 背景：桌面应用（无控制台）运行时，标准库 log 默认输出 stderr 会直接丢失，
// 出问题无从排查。本包把日志落盘到用户日志目录，并做大小轮转防止无限增长；
// 同时提供 goroutine/视图的 panic 兜底——局部崩溃记录后降级，不拖垮整个应用。
//
// 设计要点：
//   - 落盘 + stderr 双写（MultiWriter）：GUI 运行看文件，开发运行看终端；
//   - 轮转策略：app.log 超过 maxLogSize 时整体改名为 app.log.1（保留 1 份），
//     简单可靠、无外部依赖；轮转失败不阻塞日志（宁可文件大，不可丢日志）；
//   - Recover 供 goroutine 的 defer 使用：panic 记录堆栈后 goroutine 正常退出，
//     不导致进程崩溃；
//   - 所有函数并发安全（标准库 log 本身线程安全）。
package applog

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime/debug"
)

// 日志文件参数。
const (
	// FileName 是日志主文件名（位于日志目录下）。
	FileName = "app.log"
	// maxLogSize 是单文件上限（2MB）：超过即轮转为 app.log.1。
	maxLogSize = 2 * 1024 * 1024
)

var (
	// logFile 是当前持有的日志文件句柄（应用全生命周期持有；
	// Close 或再次 Setup 时释放）。
	logFile *os.File
)

// Setup 初始化全局日志：写入 dir/app.log 并镜像到 stderr。
//
// 返回日志文件完整路径，便于诊断输出。边界处理：
//   - dir 为空或创建失败 → 返回错误（调用方可降级为仅 stderr）；
//   - 已有日志超限 → 先轮转再追加；
//   - 打开失败 → 返回错误，调用方降级（日志不可用不应阻止应用启动）。
func Setup(dir string) (string, error) {
	if dir == "" {
		return "", fmt.Errorf("applog: 日志目录不能为空")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("applog: 创建日志目录失败: %w", err)
	}
	path := filepath.Join(dir, FileName)

	// 轮转失败只记录不中断：日志连续性优先于文件大小。
	if err := rotateIfNeeded(path); err != nil {
		log.Printf("applog: 日志轮转失败（已忽略）: %v", err)
	}

	// O_APPEND 保证多次打开时不互相覆盖。
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return "", fmt.Errorf("applog: 打开日志文件失败: %w", err)
	}
	// 释放上一次 Setup 的句柄（重复初始化场景，如测试反复调用）。
	if logFile != nil {
		_ = logFile.Close()
	}
	logFile = f
	log.SetOutput(io.MultiWriter(os.Stderr, f))
	// 微秒时间戳 + 文件行号：定位问题足够精确。
	log.SetFlags(log.LstdFlags | log.Lmicroseconds | log.Lshortfile)
	return path, nil
}

// Close 释放日志文件句柄并把输出恢复为仅 stderr（幂等）。
// 应用退出不必调用（进程退出自动回收）；测试与热重载场景使用，
// 同时解决 Windows 上句柄占用导致临时目录无法清理的问题。
func Close() {
	if logFile == nil {
		return
	}
	_ = logFile.Close()
	logFile = nil
	log.SetOutput(os.Stderr)
}

// rotateIfNeeded 在日志文件超限时把它改名为 app.log.1（覆盖更旧的备份）。
// 文件不存在或未超限时为 no-op。
func rotateIfNeeded(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // 首次启动：无历史日志
		}
		return err
	}
	if fi.Size() < maxLogSize {
		return nil
	}
	backup := path + ".1"
	// Windows 上目标存在时 Rename 会失败，先删旧备份（保留失败可接受）。
	_ = os.Remove(backup)
	return os.Rename(path, backup)
}

// Recover 在 goroutine 顶部 defer：panic 记录堆栈后正常返回，
// goroutine 干净退出而不使进程崩溃。
//
// 用法：
//
//	go func() {
//	    defer applog.Recover("watch-loop")
//	    ...
//	}()
func Recover(name string) {
	if r := recover(); r != nil {
		LogPanic(name, r)
	}
}

// LogPanic 记录 panic 值与完整堆栈（Recover 与视图兜底共用）。
// 独立导出：调用方可能自行 recover 后需要统一格式的记录。
func LogPanic(name string, r any) {
	// debug.Stack() 包含 panic 现场的完整调用链，是定位崩溃的关键信息。
	log.Printf("PANIC recovered in %s: %v\n%s", name, r, debug.Stack())
}
