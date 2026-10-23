package applog

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSetupWritesToFile 验证日志落盘与内容写入。
func TestSetupWritesToFile(t *testing.T) {
	dir := t.TempDir()
	path, err := Setup(dir)
	if err != nil {
		t.Fatalf("Setup 失败: %v", err)
	}
	t.Cleanup(Close) // 释放句柄，允许 TempDir 清理（Windows 句柄占用）
	if filepath.Base(path) != FileName {
		t.Fatalf("日志文件名应为 %s，实际 %s", FileName, path)
	}

	log.Print("hello-log")
	// 落盘是同步写（MultiWriter 直写文件），可立即读取校验。
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取日志失败: %v", err)
	}
	if !strings.Contains(string(data), "hello-log") {
		t.Fatalf("日志内容缺失: %q", data)
	}
}

// TestSetupRejectsEmptyDir 验证空目录参数防御。
func TestSetupRejectsEmptyDir(t *testing.T) {
	if _, err := Setup(""); err == nil {
		t.Fatal("空目录应报错")
	}
}

// TestRotateWhenOversize 验证超限轮转：旧日志保留为 app.log.1，新文件重新开始。
func TestRotateWhenOversize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)

	// 构造超限的旧日志。
	big := make([]byte, maxLogSize+1)
	for i := range big {
		big[i] = 'x'
	}
	if err := os.WriteFile(path, big, 0o644); err != nil {
		t.Fatalf("准备旧日志失败: %v", err)
	}

	// Setup 应触发轮转。
	rotated, err := Setup(dir)
	if err != nil {
		t.Fatalf("Setup 失败: %v", err)
	}
	t.Cleanup(Close) // 释放句柄，允许 TempDir 清理
	// 旧内容应在备份中。
	backup, err := os.ReadFile(rotated + ".1")
	if err != nil {
		t.Fatalf("备份文件不存在: %v", err)
	}
	if len(backup) != maxLogSize+1 {
		t.Fatalf("备份内容大小异常: %d", len(backup))
	}
	// 新日志已重新开始（不含旧内容）。
	data, err := os.ReadFile(rotated)
	if err != nil {
		t.Fatalf("读取新日志失败: %v", err)
	}
	if strings.Contains(string(data), "xxxxx") {
		t.Fatal("新日志不应包含轮转前内容")
	}
}

// TestNoRotateWhenSmall 验证未超限不轮转。
func TestNoRotateWhenSmall(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	if err := os.WriteFile(path, []byte("small"), 0o644); err != nil {
		t.Fatalf("准备日志失败: %v", err)
	}
	if err := rotateIfNeeded(path); err != nil {
		t.Fatalf("rotateIfNeeded 失败: %v", err)
	}
	if _, err := os.Stat(path + ".1"); !os.IsNotExist(err) {
		t.Fatal("未超限不应产生备份文件")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "small" {
		t.Fatalf("未超限时内容不应变化: %q", data)
	}
}

// TestRotateMissingFileIsNoop 验证文件不存在时轮转为 no-op（首次启动）。
func TestRotateMissingFileIsNoop(t *testing.T) {
	if err := rotateIfNeeded(filepath.Join(t.TempDir(), "nope.log")); err != nil {
		t.Fatalf("不存在的文件应 no-op，实际: %v", err)
	}
}
