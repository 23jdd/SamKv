package wal_test

// 本文件展示 WAL 记录编解码和严格持久化写入。
// 临时目录会在 Example 结束后删除，输出不依赖平台路径。

import (
	"bytes"
	"fmt"
	"os"

	"github.com/23jdd/SamKv/pkg/wal"
)

func ExampleRecord() {
	record := wal.PutRecord([]byte("key"), []byte("value"))
	record.Sequence = 7
	encoded, err := record.Encode()
	if err != nil {
		panic(err)
	}
	decoded, err := wal.Decode(encoded)
	if err != nil {
		panic(err)
	}
	fmt.Println(decoded.Type, decoded.Sequence, string(decoded.Key), string(decoded.Value))

	// Output:
	// 1 7 key value
}

func ExampleWalManger() {
	dir, err := os.MkdirTemp("", "samkv-wal-example-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)

	options := wal.DefaultOptions()
	options.SyncPolicy = wal.SyncEveryWrite
	manager, err := wal.NewWithOptions(dir, options)
	if err != nil {
		panic(err)
	}
	if err := manager.AppendRecord(wal.PutRecord([]byte("durable"), []byte("yes"))); err != nil {
		panic(err)
	}
	if err := manager.Close(); err != nil {
		panic(err)
	}

	segments, err := wal.ListSegments(dir)
	if err != nil || len(segments) != 1 {
		panic("unexpected WAL segments")
	}
	data, err := os.ReadFile(segments[0].Path)
	if err != nil {
		panic(err)
	}
	record, err := wal.ReadRecord(bytes.NewReader(data))
	if err != nil {
		panic(err)
	}
	fmt.Println(string(record.Key), string(record.Value))

	// Output:
	// durable yes
}
