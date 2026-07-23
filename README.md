# SamKv

SamKV 是一个面向日志场景的单机 LSM-Tree KV 存储引擎。它已经实现从 WAL、MemTable、Immutable MemTable 到 SSTable、Manifest、范围查询和 Compaction 的完整本地持久化链路。

## 已实现能力

- WAL 顺序写入、CRC32 校验、后台刷盘、超 4 KiB 单记录直写
- 崩溃恢复、半条 WAL 尾记录修复、WAL 原子重写
- 并发安全 SkipList MemTable、原子大小统计、墓碑
- 达到阈值后切换 Immutable MemTable，后台生成 SSTable
- SSTable DataBlock、MetaBlock、IndexBlock、Footer 和 6 字节 Magic
- DataBlock 前缀压缩和 restart point
- key BloomFilter、标签 BloomFilter、时间范围和标签基数元数据
- Manifest 原子发布、备份恢复、SSTable 文件编号和层级记录
- 点查询、通用 key 范围扫描、时间范围与标签子集查询
- 时间 + 有序标签 + 唯一序列号复合 key
- Gzip 压缩日志 value
- 通用 Batch 和结构化日志 Batch
- 全量 Compaction、版本覆盖、墓碑回收
- 按时间和近似容量执行日志保留
- 自动 Compaction 和运行状态统计
- Gin HTTP KV API、请求大小限制和优雅关闭

## 快速使用

```go
package main

import (
	"fmt"
	"log"
	"time"

	"github.com/23jdd/SamKv/pkg/store"
	"github.com/23jdd/SamKv/pkg/utils"
)

func main() {
	options := store.DefaultOptions()
	options.MemTableLimit = 4 * 1024 * 1024
	options.CompactionThreshold = 4
	options.Retention = 7 * 24 * time.Hour
	options.MaxSizeBytes = 10 * 1024 * 1024 * 1024

	db, err := store.NewStoreManagerWithOptions("./data", options)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	_, err = db.WriteLog(store.LogEntry{
		Timestamp: time.Now().UTC(),
		Labels: []utils.Label{
			{Name: "app", Value: "nginx"},
			{Name: "level", Value: "ERROR"},
		},
		Message: []byte("upstream connection failed"),
	})
	if err != nil {
		log.Fatal(err)
	}

	end := time.Now().UTC()
	start := end.Add(-time.Hour)
	logs, err := db.Query(start, end, []utils.Label{
		{Name: "app", Value: "nginx"},
		{Name: "level", Value: "ERROR"},
	})
	if err != nil {
		log.Fatal(err)
	}
	for _, entry := range logs {
		fmt.Printf("%s %s
", entry.Timestamp.Format(time.RFC3339Nano), entry.Message)
	}
}
```

`Query` 的时间范围是闭区间 `[startTime, endTime]`。标签使用子集匹配，例如只传 `app=nginx` 会返回所有同时包含该标签的日志。

## 普通 KV

```go
db, err := store.NewStoreManager("./data", 4*1024*1024)
if err != nil {
	panic(err)
}
defer db.Close()

_ = db.Put("key", "value")
value, ok := db.Get("key")
_ = value
_ = ok

_ = db.Delete("key")
records, err := db.Scan("a", "z") // [a, z)
_ = records
_ = err
```

## HTTP API

服务默认读取 `.env`，监听 `Address:Port`，数据保存在 `dir` 指定的目录。启动：

```bash
go run .
```

```bash
curl -X PUT http://127.0.0.1:9999/kv/app/config -H "Content-Type: application/json" -d '{"value":"enabled"}'
curl http://127.0.0.1:9999/kv/app/config
curl -X DELETE http://127.0.0.1:9999/kv/app/config
curl http://127.0.0.1:9999/healthz
```

`PUT` 和 `DELETE` 成功返回 `204`，`GET` 成功返回 `{"key":"app/config","value":"enabled"}`，不存在的 key 返回 `404`。`/kv/*key` 支持包含 `/` 的 key。

## CLI

`samctl` 目录提供一个无额外依赖的 HTTP 客户端：
## Install  
```bash
go install ./samctl
```
```bash
samctl put app/config enabled
samctl get app/config
samctl  del app/config
samctl health
```

也可以使用参数模式，并通过 `-a`、`-p`、`-timeout` 指定服务：

```bash
samctl -m put -k app/config -v enabled -a 127.0.0.1 -p 9999
samctl get -a 127.0.0.1 -p 9999 app/config
```

`get` 只向标准输出写 value，`put`、`del` 成功输出 `ok`，便于在脚本中使用。

## 批量写

```go
batch := store.NewBatch().
	Put("a", "1").
	Put("b", "2").
	Delete("a")

if err := db.WriteBatch(batch); err != nil {
	panic(err)
}
```

结构化日志可以使用 `WriteLogs`，多条记录会合并成一次 WAL 追加：

```go
sequences, err := db.WriteLogs([]store.LogEntry{
	{Timestamp: time.Now(), Labels: labels, Message: []byte("first")},
	{Timestamp: time.Now(), Labels: labels, Message: []byte("second")},
})
_ = sequences
_ = err
```

Batch 会在进程内按顺序整体应用，并减少 WAL 提交和锁竞争。WAL 恢复仍按单条记录重放，因此它不是跨记录事务协议。

## 配置

```go
type Options struct {
	MemTableLimit       int
	AutoCheckpoint      bool
	CompactionThreshold int
	Retention           time.Duration
	MaxSizeBytes        int64
}
```

- `MemTableLimit`：活动 MemTable 的近似字节阈值；`0` 表示不按大小自动切换。
- `AutoCheckpoint`：达到阈值后是否切换 Immutable MemTable 并后台刷盘。
- `CompactionThreshold`：SSTable 数量达到该值后自动 Compaction；`0` 表示关闭。
- `Retention`：Compaction 时删除早于保留窗口的结构化日志。
- `MaxSizeBytes`：Compaction 时按时间从旧到新淘汰日志，直到近似记录大小不超过限制。

`DefaultOptions` 默认启用 4 MiB MemTable 和 4 张 SSTable 的自动 Compaction 阈值。

## Key 与 Value

结构化日志 key 的二进制布局：

```text
[8 bytes ordered timestamp][sorted label_name=label_value][0x00][8 bytes sequence]
```

时间戳使用翻转符号位后的 big-endian `int64`，因此有符号时间顺序与字节序一致。标签按名称和值排序，并对 `%`、`|`、`=` 转义。序列号由 Store 自动递增，也可以由调用方显式指定。

Value 布局：

```text
[version][compression][timestamp][message length][compressed message]
```

标签只保存在 key 中。当前支持原文和 Gzip，默认使用 Gzip。

## SSTable 格式

```text
[DataBlock 1] ... [DataBlock N]
[MetaBlock]
[IndexBlock]
[Footer]
```

- `DataBlock`：有序记录、前缀压缩、restart point、墓碑标记。
- `MetaBlock`：key 范围、时间范围、记录数、key/标签 BloomFilter、标签基数。
- `IndexBlock`：每个 DataBlock 的首尾 key、偏移和大小。
- `Footer`：6 字节 Magic `流萤`、版本号、MetaBlock 和 IndexBlock 位置。

打开 SSTable 时只加载 Footer、MetaBlock 和 IndexBlock，DataBlock 在查询时按需读取。

## 持久化顺序

```text
WAL -> active MemTable -> Immutable MemTable -> SSTable -> MANIFEST
```

后台刷盘发布 SSTable 后，会根据仍未落盘的 MemTable 重写 WAL。关键崩溃点都保留可恢复副本：

- SSTable 发布前崩溃：旧 WAL 恢复全部记录。
- Manifest 发布后、WAL 裁剪前崩溃：SSTable 与旧 WAL 可能重复，但查询结果一致。
- 替换 Manifest 或 WAL 中途崩溃：`.bak` 文件用于恢复。
- WAL 尾部只有半条记录：启动时截断到最后一条完整记录。

`Close` 会停止后台任务并刷出 WAL 缓冲，但不会强制生成 SSTable；下次打开会自动回放 WAL。需要明确落成 SSTable 时调用 `Checkpoint`。

## Compaction 与统计

```go
result, err := db.Compact()
stats := db.Stats()
_ = result
_ = err
_ = stats
```

Compaction 合并当前全部 SSTable，因此可以安全删除被覆盖版本和墓碑。结构化日志同时应用时间和容量保留。输出文件记录为 L1，新刷盘文件记录为 L0。

`Stats` 提供读写次数、Checkpoint/Compaction 次数、活动和只读 MemTable、WAL/SSTable 字节数、层级文件数及后台错误。

## 测试

```bash
go test ./...
go test -race ./pkg/store ./pkg/wal ./pkg/skipList
go vet ./...
```

测试覆盖 WAL 大记录、崩溃尾部、Manifest 连续替换、SSTable 元数据、墓碑、范围合并、自动刷盘、并发 MemTable 切换、批量恢复、Compaction 和保留策略。

## 当前边界

当前实现是单进程、本地文件系统存储。L0/L1 已提供本地冷热层级语义，但 SSD/HDD 分目录、对象存储迁移、分片和分布式副本不属于本地存储内核，由上层部署和后续适配器负责。