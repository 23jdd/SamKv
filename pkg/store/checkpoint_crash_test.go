package store

// 本文件模拟 Checkpoint 两阶段发布后、旧 WAL 删除前崩溃的恢复窗口。

import (
	"os"
	"testing"

	"github.com/23jdd/SamKv/pkg/wal"
)

func TestCheckpointRecoveryToleratesPublishedManifestWithOldWAL(t *testing.T) {
	dir := t.TempDir()
	options := DefaultOptions()
	options.AutoCheckpoint = false
	options.CompactionThreshold = 0

	database, err := NewStoreManagerWithOptions(dir, options)
	if err != nil {
		t.Fatal(err)
	}
	putStore(t, database, "k1", "before-checkpoint")
	if err := database.wm.Flush(); err != nil {
		t.Fatal(err)
	}
	oldWAL, err := os.ReadFile(wal.SegmentPath(dir, 1))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	putStore(t, database, "k2", "after-checkpoint")
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	// 模拟 Manifest 已原子发布，但进程在删除旧 segment 之前退出。
	if err := os.WriteFile(wal.SegmentPath(dir, 1), oldWAL, 0644); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewStoreManagerWithOptions(dir, options)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := getStore(t, reopened, "k2"); !ok || got != "after-checkpoint" {
		t.Fatalf("Get(k2) = %q, %v; want after-checkpoint, true", got, ok)
	}

	// 再次发布后，重复旧段和包含最新更新的段都可安全回收。
	if _, err := reopened.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	segments, err := wal.ListSegments(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(segments) != 1 || segments[0].ID != 3 {
		t.Fatalf("segments after recovery checkpoint = %+v, want only segment 3", segments)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}

	verified, err := NewStoreManagerWithOptions(dir, options)
	if err != nil {
		t.Fatal(err)
	}
	defer verified.Close()
	if got, ok := getStore(t, verified, "k2"); !ok || got != "after-checkpoint" {
		t.Fatalf("Get(k2) after second restart = %q, %v; want after-checkpoint, true", got, ok)
	}
}
