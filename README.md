<p align="center">
  <img src="./logo.png" alt="SamKv Logo" width="220">
</p>

# SamKv

SamKv 是一个使用 Go 实现、面向日志场景的单机 LSM-Tree KV 存储引擎。项目已经打通 WAL、MemTable、Immutable MemTable、SSTable、Manifest、查询与 Compaction 的本地持久化链路，并提供 Gin HTTP API 和命令行客户端。

## 目录

- [核心能力](#核心能力)
- [运行服务](#运行服务)
- [HTTP API](#http-api)
- [命令行工具](#命令行工具)
- [Go API](#go-api)
- [查询表达式](#查询表达式)
- [配置](#配置)
- [存储设计](#存储设计)
- [测试](#测试)
- [当前边界](#当前边界)

## 核心能力

| 模块 | 已实现能力 |
| --- | --- |
| WAL | 顺序写、CRC32 校验、50 ms 后台刷盘、超过 4 KiB 的单记录直写、损坏尾部修复、原子重写 |
| MemTable | 并发安全 SkipList、原子大小统计、墓碑、Mutable/Immutable 切换与后台刷盘 |
| SSTable | DataBlock、MetaBlock、IndexBlock、Footer、前缀压缩、restart point、按需读取 |
| 索引 | key BloomFilter、标签 BloomFilter、时间范围、标签基数与 DataBlock key 范围索引 |
| 元数据 | Manifest 原子发布、备份恢复、SSTable 文件编号、层级和日志序列号记录 |
| 查询 | 点查询、通用 key 范围扫描、时间范围查询、标签子集匹配、Participle 查询表达式解析 |
| 维护 | Checkpoint、全量 Compaction、版本覆盖、墓碑回收、时间和容量保留策略、运行统计 |
| 接入 | Gin HTTP KV API、请求大小限制、健康检查、优雅关闭、`samctl` 命令行工具 |

## 运行服务

环境要求：Go 1.25.1 或兼容版本。

仓库根目录的 [`.env`](./.env) 已提供一组本地配置。启动 HTTP 服务：

```bash
go run .
```

默认示例配置监听 `0.0.0.0:9999`，数据写入 `./logs`。服务收到 `SIGINT` 或 `SIGTERM` 后会优雅关闭 Store 和 HTTP Server。

写入链路：

```text
HTTP / Go API
      |
      v
WAL -> Active MemTable -> Immutable MemTable -> SSTable -> MANIFEST
```

读取时依次合并 Active MemTable、Immutable MemTable 和 SSTable 中的版本，并应用最新值与墓碑。

## HTTP API

| 方法 | 路径 | 请求体 | 成功响应 |
| --- | --- | --- | --- |
| `GET` | `/healthz` | 无 | `200 {"status":"ok"}` |
| `PUT` | `/kv/*key` | `{"value":"..."}` | `204 No Content` |
| `GET` | `/kv/*key` | 无 | `200 {"key":"...","value":"..."}` |
| `DELETE` | `/kv/*key` | 无 | `204 No Content` |

`*key` 可以包含 `/`。缺少 key 返回 `400`，key 不存在返回 `404`，后台维护失败时健康检查返回 `503`。HTTP 请求体以及 HTTP 层允许的编码后 WAL 记录上限均为 64 MiB。

```bash
curl -X PUT http://127.0.0.1:9999/kv/app/config \
  -H "Content-Type: application/json" \
  -d '{"value":"enabled"}'

curl http://127.0.0.1:9999/kv/app/config
curl -X DELETE http://127.0.0.1:9999/kv/app/config
curl http://127.0.0.1:9999/healthz
```

## 命令行工具

安装仓库内的 `samctl`：

```bash
go install ./samctl
```

默认连接 `127.0.0.1:9999`：

```bash
samctl put app/config enabled
samctl get app/config
samctl del app/config
samctl health
```

也可以指定地址、端口和超时时间，或使用参数模式：

```bash
samctl get -a 127.0.0.1 -p 9999 -timeout 5s app/config
samctl -m put -k app/config -v enabled -a 127.0.0.1 -p 9999
```

`get` 只向标准输出写 value；`put` 和 `del` 成功时输出 `ok`，便于在脚本中使用。

## Go API

### 普通 KV

```go
db, err := store.NewStoreManager("./data", 4*1024*1024)
if err != nil {
	panic(err)
}
defer db.Close()

if err := db.Put("key", "value"); err != nil {
	panic(err)
}

value, found := db.Get("key")
_ = value
_ = found

if err := db.Delete("key"); err != nil {
	panic(err)
}

records, err := db.Scan("a", "z") // 半开区间 [a, z)
_ = records
_ = err
```

### 结构化日志

```go
options := store.DefaultOptions()
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
logs, err := db.Query(end.Add(-time.Hour), end, []utils.Label{
	{Name: "app", Value: "nginx"},
})
if err != nil {
	log.Fatal(err)
}

for _, entry := range logs {
	fmt.Printf("%s %s\n", entry.Timestamp.Format(time.RFC3339Nano), entry.Message)
}
```

`Query` 使用闭区间 `[startTime, endTime]`。标签为子集匹配，只传 `app=nginx` 会返回所有同时包含该标签的日志。

### 批量写与维护

```go
batch := store.NewBatch().
	Put("a", "1").
	Put("b", "2").
	Delete("a")

if err := db.WriteBatch(batch); err != nil {
	panic(err)
}

sequences, err := db.WriteLogs([]store.LogEntry{
	{Timestamp: time.Now(), Labels: labels, Message: []byte("first")},
	{Timestamp: time.Now(), Labels: labels, Message: []byte("second")},
})
if err != nil {
	panic(err)
}
fmt.Println(sequences)

sstablePath, err := db.Checkpoint()
if err != nil {
	panic(err)
}
fmt.Println(sstablePath)

result, err := db.Compact()
if err != nil {
	panic(err)
}
fmt.Println(result, db.Stats())
```

`WriteBatch` 会将整批 WAL 数据一次追加，再按顺序更新 MemTable，从而减少提交和锁竞争；WAL 恢复仍按单条记录重放，因此它不是跨记录事务协议。

`Checkpoint` 把当前内存数据落成 SSTable 并重写 WAL。`Compact` 合并全部 SSTable、清理旧版本和墓碑，并应用日志保留策略。`Stats` 返回操作次数、MemTable、WAL、SSTable、层级和后台错误等运行状态。

## 查询表达式

[`pkg/parse`](./pkg/parse) 使用 Participle 解析以下 matcher 字符串：

```text
matcher{label=value,...}[range] offset duration
```

示例：

```text
error{app=nginx}[5m]
"upstream connection failed"{app=nginx,level="ERROR"}[5m] offset 1h
```

- `matcher` 是要匹配的日志内容，可以是标识符、数字或带引号字符串。
- 标签只支持等值匹配，标签名不能重复；`{}` 表示不限制标签。
- `range` 必须大于 0，格式遵循 `time.ParseDuration`。
- `offset` 可选，用于把查询时间窗口向过去平移。

```go
query, err := parse.ParseQueryFormat(
	`"upstream connection failed"{app=nginx,level="ERROR"}[5m] offset 1h`,
)
if err != nil {
	return err
}

start, end := query.TimeRange(time.Now().UTC())
labels := make([]utils.Label, 0, len(query.Labels))
for _, label := range query.Labels {
	labels = append(labels, utils.Label{Name: label.Name, Value: label.Value})
}
entries, err := db.Query(start, end, labels)
```

当前 `Store.Query` 负责时间和标签过滤，`QueryFormat.Query` 中的 matcher 由调用层用于日志内容匹配。HTTP 层目前只暴露普通 KV 接口，尚未暴露日志查询接口。

## 配置

库配置类型：

```go
type Options struct {
	MemTableLimit       int
	AutoCheckpoint      bool
	CompactionThreshold int
	Retention           time.Duration
	MaxSizeBytes        int64
}
```

| 字段 | `DefaultOptions()` | 说明 |
| --- | ---: | --- |
| `MemTableLimit` | 4 MiB | Active MemTable 的近似字节阈值，`0` 表示不按大小自动切换 |
| `AutoCheckpoint` | `true` | 达到阈值后切换 Immutable MemTable 并在后台刷盘 |
| `CompactionThreshold` | `4` | SSTable 数量达到阈值后自动 Compaction，`0` 表示关闭 |
| `Retention` | `0` | Compaction 时删除超过保留时长的结构化日志，`0` 表示永久保留 |
| `MaxSizeBytes` | `0` | Compaction 时从旧到新淘汰日志，`0` 表示不限制容量 |

根目录 `.env` 用于配置服务。仓库当前示例采用 `MemTableLimit=4096`、`CompactionThreshold=10`、`Retention=144` 小时，方便较快触发本地刷盘并保留 6 天日志；这些值与库默认值是两个独立概念。

| 环境变量 | 说明 |
| --- | --- |
| `dir` | 数据目录；未配置时服务使用 `./data` |
| `Address` / `Port` | HTTP 监听地址和端口，代码默认值为 `0.0.0.0:9999` |
| `MemTableLimit` | 对应 `Options.MemTableLimit`，单位为字节 |
| `AutoCheckpoint` | 对应 `Options.AutoCheckpoint` |
| `CompactionThreshold` | 对应 `Options.CompactionThreshold` |
| `Retention` | 对应 `Options.Retention`，单位为小时 |
| `MaxSizeBytes` | 对应 `Options.MaxSizeBytes`，单位为字节 |

## 存储设计

### Key 与 Value

结构化日志 key 的二进制布局：

```text
[8 bytes ordered timestamp][sorted label_name=label_value][0x00][8 bytes sequence]
```

时间戳是翻转符号位后的 big-endian `int64`，因此有符号时间顺序与字节序一致。标签按名称和值排序，并对 `%`、`|`、`=` 转义。序列号由 Store 自动递增，也可以由调用方显式指定。

Value 不重复保存标签，布局如下：

```text
[version][compression][timestamp][message length][compressed message]
```

当前支持原文和 Gzip，默认使用 Gzip。

### SSTable

```text
[DataBlock 1] ... [DataBlock N]
[MetaBlock]
[IndexBlock]
[Footer]
```

| Block | 内容 |
| --- | --- |
| DataBlock | 有序记录、前缀压缩、restart point 和墓碑标记 |
| MetaBlock | key 范围、时间范围、记录数、key/标签 BloomFilter 和标签基数 |
| IndexBlock | 每个 DataBlock 的首尾 key、偏移和大小 |
| Footer | 6 字节 UTF-8 Magic `流萤`、版本号、MetaBlock 和 IndexBlock 位置 |

打开 SSTable 时只加载 Footer、MetaBlock 和 IndexBlock，DataBlock 在查询时按需读取。Magic 固定占 Footer 的前 6 字节，用于拒绝错误文件或不兼容格式。

### Manifest 与恢复

`MANIFEST` 是 Store 已发布 SSTable 的权威目录，记录文件名、层级、key/时间范围、记录数、下一个文件编号和最后使用的日志序列号。WAL 恢复尚未 Checkpoint 的数据，Manifest 恢复已经写成 SSTable 的数据。

后台刷盘发布 SSTable 后，会根据仍未落盘的 MemTable 原子重写 WAL。主要崩溃场景的恢复策略：

- SSTable 发布前崩溃：使用旧 WAL 恢复全部记录。
- Manifest 发布后、WAL 裁剪前崩溃：SSTable 与 WAL 可能重复，但读取时按版本合并。
- 替换 Manifest 或 WAL 时崩溃：尝试使用 `.bak` 文件恢复。
- WAL 尾部只有半条记录：启动时截断到最后一条完整记录。

`Close` 会停止后台任务并刷出 WAL 缓冲，但不会强制生成 SSTable。需要明确落盘成 SSTable 时调用 `Checkpoint`。

## 测试

```bash
go test ./...
go test -race ./pkg/store ./pkg/wal ./pkg/skipList
go vet ./...
```

测试覆盖 WAL 大记录和损坏尾部、Manifest 连续替换、SSTable 元数据、BloomFilter、墓碑、范围合并、自动刷盘、并发 MemTable 切换、批量恢复、Compaction、保留策略、HTTP 路由、CLI 和查询表达式解析。

## 当前边界

SamKv 当前是单进程、本地文件系统存储。L0/L1 提供本地层级语义，但还没有 SSD/HDD 分目录、对象存储迁移、分片和分布式副本。HTTP API 目前只提供通用 KV 操作，结构化日志写入、范围查询和 matcher 内容过滤仍通过 Go API 或上层服务完成。
