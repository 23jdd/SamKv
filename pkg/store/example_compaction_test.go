package store_test

// 本文件演示如何显式触发 L0 Compaction，并观察并行子任务及多 SSTable 输出。

import (
	"fmt"
	"os"

	"github.com/23jdd/SamKv/pkg/store"
)

// ExampleStoreManger_CompactLevel 演示把两个互不重叠的 L0 文件并行合并到 L1。
// 生产环境通常由后台任务自动触发；显式调用适合维护命令和确定性测试。
func ExampleStoreManger_CompactLevel() {
	dir, err := os.MkdirTemp("", "samkv-compaction-example-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)

	options := store.DefaultOptions()
	options.AutoCheckpoint = false
	options.CompactionThreshold = 0
	options.CompactionWorkers = 2
	options.CompactionTaskBytes = 1

	database, err := store.NewStoreManagerWithOptions(dir, options)
	if err != nil {
		panic(err)
	}
	defer database.Close()

	_ = database.Put("a", "1")
	_, _ = database.Checkpoint()
	_ = database.Put("z", "2")
	_, _ = database.Checkpoint()

	result, err := database.CompactLevel(0)
	fmt.Println(result.SourceLevel, result.TargetLevel, result.Subtasks, result.OutputTables, err)
	// Output:
	// 0 1 2 2 <nil>
}
