package store_test

// 本文件演示创建、校验、恢复备份，并从恢复目录重新打开数据。

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/23jdd/SamKv/pkg/store"
)

// ExampleStoreManger_Backup 展示备份恢复的完整最小流程。
func ExampleStoreManger_Backup() {
	root, err := os.MkdirTemp("", "samkv-backup-example-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(root)

	dataDir := filepath.Join(root, "data")
	backupDir := filepath.Join(root, "backup")
	restoreDir := filepath.Join(root, "restored")
	database, err := store.NewStoreManager(dataDir, 1024)
	if err != nil {
		panic(err)
	}
	if err := database.WriteBatch(store.NewBatch().Put("release", "v1")); err != nil {
		panic(err)
	}

	metadata, backupErr := database.Backup(backupDir)
	_, verifyErr := store.VerifyBackup(backupDir)
	restoreErr := store.RestoreBackup(backupDir, restoreDir)
	_ = database.Close()

	restored, openErr := store.NewStoreManager(restoreDir, 1024)
	if openErr != nil {
		panic(openErr)
	}
	records, _ := restored.Scan("release", "release\x00")
	value, found := "", false
	if len(records) > 0 && !records[0].Deleted {
		value, found = records[0].Val, true
	}
	_ = restored.Close()

	fmt.Println(len(metadata.Files), backupErr, verifyErr, restoreErr)
	fmt.Println(value, found)
	// Output:
	// 4 <nil> <nil> <nil>
	// v1 true
}
