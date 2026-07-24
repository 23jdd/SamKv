<p align="center">
  <img src="./logo.png" alt="SamKv Logo" width="220">
</p>

# SamKv

SamKv 是一个使用 Go 实现、面向结构化日志场景的单机 LSM-Tree KV 存储引擎。项目已经打通 WAL、并发 MemTable、SSTable、Manifest、Block Cache、分层 Compaction、完整性检查、备份恢复与 HTTP 服务，可作为本地嵌入式存储和继续演进的基础。

## 目录

- [核心能力](#核心能力)
- [快速开始](#快速开始)
- [HTTP API](#http-api)
- [命令行工具](#命令行工具)
- [Go API](#go-api)
- [QueryFormat](#queryformat)
- [配置](#配置)
- [存储与恢复](#存储与恢复)
- [测试与压测](#测试与压测)
- [当前边界](#当前边界)

## 核心能力

| 模块 | 已实现能力 |
| --- | --- |
| WAL | CRC32 记录校验、损坏尾部截断、超过缓冲容量的记录直写、周期 fsync 与每写 fsync 两种策略 |
| MemTable | 并发安全 SkipList、原子容量统计、墓碑、Mutable/Immutable 切换和后台刷盘 |
| SSTable | DataBlock、MetaBlock、IndexBlock、Footer、前缀压缩、restart point、CRC32C Block 校验 |
| 索引与缓存 | key/标签 BloomFilter、时间与 key 范围索引、共享字节容量 LRU Block Cache |
| Compaction | L0 重叠合并、非零层增量下推、层容量阈值、底层墓碑与保留策略回收 |
| 元数据 | Manifest 原子发布与备份、格式版本、SSTable 层级和日志序列号 |
| 运维 | 数据目录进程锁、Checkpoint、校验修复、全量备份恢复、格式升级、运行指标 |
| 接入 | KV HTTP API、结构化日志写入/批量写入/QueryFormat 查询、Prometheus 指标、CLI |

## 快速开始

环境要求：Go 1.25.1 或兼容版本。

```bash
go run .
```

默认示例配置监听 `0.0.0.0:9999`，数据写入 `./logs`。服务收到 `SIGINT` 或 `SIGTERM` 后会优雅关闭 HTTP Server 和 Store。

```text
HTTP / Go API
      |
      v
WAL -> Active MemTable -> Immutable MemTable -> L0 SSTable
                                                |
                                                v
                                      L1 -> L2 -> L3
```

同一个数据目录只能由一个 Store 进程打开。第二个进程会收到 `store: data directory is locked`，`LOCK` 文件中保留锁持有者信息。

## HTTP API

### 普通 KV

| 方法 | 路径 | 请求体 | 成功响应 |
| --- | --- | --- | --- |
| `GET` | `/healthz` | 无 | `200 {"status":"ok"}` |
| `PUT` | `/kv/*key` | `{"value":"..."}` | `204 No Content` |
| `GET` | `/kv/*key` | 无 | `200 {"key":"...","value":"..."}` |
| `DELETE` | `/kv/*key` | 无 | `204 No Content` |

`*key` 可以包含 `/`。缺少 key 返回 `400`，key 不存在返回 `404`，SSTable 读取损坏等错误返回 `500`，健康检查在 Store 异常时返回 `503`。HTTP 请求体和编码后的 WAL 单条记录上限均为 64 MiB。

```bash
curl -X PUT http://127.0.0.1:9999/kv/app/config \
  -H "Content-Type: application/json" \
  -d '{"value":"enabled"}'

curl http://127.0.0.1:9999/kv/app/config
curl -X DELETE http://127.0.0.1:9999/kv/app/config
```

### 结构化日志写入

```bash
curl -X POST http://127.0.0.1:9999/logs \
  -H "Content-Type: application/json" \
  -d '{
    "timestamp":"2026-07-24T10:30:00Z",
    "labels":{"app":"nginx","level":"ERROR","host":"server1"},
    "message":"upstream connection failed"
  }'
```

成功返回 `201` 和自动分配的唯一序列号：

```json
{"sequence":1}
```

`timestamp` 可省略，服务会使用当前 UTC 时间。`sequence` 可省略或设为 `0`，由 Store 自动分配。批量写入最多接受 10,000 条：

```bash
curl -X POST http://127.0.0.1:9999/logs/batch \
  -H "Content-Type: application/json" \
  -d '{
    "entries":[
      {"labels":{"app":"api"},"message":"request started"},
      {"labels":{"app":"api","level":"ERROR"},"message":"request failed"}
    ]
  }'
```

### 结构化日志查询

`query` 参数使用 [QueryFormat](#queryformat)。matcher 会对日志 `message` 执行区分大小写的字节子串过滤，标签执行等值子集匹配：

```bash
curl -G http://127.0.0.1:9999/logs/query \
  --data-urlencode 'query="upstream connection failed"{app=nginx,level=ERROR}[1h]' \
  --data-urlencode 'limit=100'
```

响应包含实际时间窗口、matcher、结果和是否被截断：

```json
{
  "matcher":"upstream connection failed",
  "start":"2026-07-24T09:30:00Z",
  "end":"2026-07-24T10:30:00Z",
  "entries":[
    {
      "timestamp":"2026-07-24T10:30:00Z",
      "labels":{"app":"nginx","level":"ERROR","host":"server1"},
      "message":"upstream connection failed",
      "sequence":1
    }
  ],
  "truncated":false
}
```

`limit` 默认 1,000，取值范围为 1 到 10,000。

### 指标

```bash
curl http://127.0.0.1:9999/metrics
```

`/metrics` 使用 Prometheus 文本格式，包含读写、Checkpoint、Compaction、MemTable、WAL/SSTable 字节数、每层文件数、Block Cache 命中/未命中/淘汰以及后台错误状态。指标为进程内统计，重启后计数器重新开始。

## 命令行工具

### samctl

```bash
go install ./samctl

samctl put app/config enabled
samctl get app/config
samctl del app/config
samctl health
```

默认连接 `127.0.0.1:9999`。也可以指定地址、端口和超时：

```bash
samctl get -a 127.0.0.1 -p 9999 -timeout 5s app/config
samctl -m put -k app/config -v enabled -a 127.0.0.1 -p 9999
```

### samkv-admin

维护命令要求服务已停止；目录锁会阻止管理工具和服务同时打开数据目录。

```bash
go install ./cmd/samkv-admin

samkv-admin verify -dir ./logs
samkv-admin repair -dir ./logs
samkv-admin backup -dir ./logs -dest ./backup-20260724
samkv-admin verify-backup -source ./backup-20260724
samkv-admin restore -source ./backup-20260724 -dest ./restored
samkv-admin upgrade -dir ./logs
```

- `verify` 校验全部 DataBlock、记录顺序、元数据范围和 BloomFilter。
- `repair` 以 Manifest 为权威来源，重建 Manifest，并把损坏 SSTable 移到 `corrupt/`；它无法恢复已经损坏的数据。
- `backup` 先执行 Checkpoint，再复制 Manifest、WAL 和已发布 SSTable，并在 `BACKUP.json` 保存 SHA-256。
- `restore` 只恢复到尚不存在的目录，发布前会完整校验备份。
- `upgrade` 将兼容读取的旧 SSTable 重写为当前格式。

## Go API

### 打开与持久性

```go
options := store.DefaultOptions()
options.WALSyncPolicy = store.WALSyncEveryWrite
options.Retention = 7 * 24 * time.Hour
options.MaxSizeBytes = 10 * 1024 * 1024 * 1024

db, err := store.NewStoreManagerWithOptions("./data", options)
if err != nil {
    log.Fatal(err)
}
defer db.Close()
```

WAL 有两种明确策略：

- `WALSyncInterval`：默认每 50 ms 执行一次 fsync。写入延迟较低，但操作系统或机器崩溃时可能丢失最近一个同步周期的数据。
- `WALSyncEveryWrite`：每次写操作都在返回前完成 fsync，延迟更高，但返回成功的数据已经交给文件系统同步。

两种策略在正常 `Close` 时都会刷新缓冲并同步。Checkpoint 是把内存数据发布为 SSTable 并裁剪 WAL，不是替代 WAL fsync 的提交协议。

### KV 与日志

```go
if err := db.Put("key", "value"); err != nil {
    panic(err)
}

value, found, err := db.GetWithError("key")
records, err := db.Scan("a", "z") // 半开区间 [a, z)

sequence, err := db.WriteLog(store.LogEntry{
    Timestamp: time.Now().UTC(),
    Labels: []utils.Label{
        {Name: "app", Value: "nginx"},
        {Name: "level", Value: "ERROR"},
    },
    Message: []byte("upstream connection failed"),
})

end := time.Now().UTC()
logs, err := db.Query(end.Add(-time.Hour), end, []utils.Label{
    {Name: "app", Value: "nginx"},
})

_, _, _, _, _, _ = value, found, records, sequence, logs, err
```

`Get` 为兼容旧调用保留；需要区分“不存在”和“读取损坏”时应使用 `GetWithError`。`Query` 使用闭区间 `[startTime, endTime]`，标签是子集匹配。

### 批量、Compaction 与维护

```go
batch := store.NewBatch().
    Put("a", "1").
    Put("b", "2").
    Delete("a")

if err := db.WriteBatch(batch); err != nil {
    panic(err)
}

_, err := db.Checkpoint()
result, err := db.CompactNextLevel()
result, err = db.CompactLevel(0)
result, err = db.Compact() // 显式全量合并

verification, err := db.Verify()
backup, err := db.Backup("./backup-20260724")
upgrade, err := db.UpgradeFormat()
stats := db.Stats()

_, _, _, _, _, _ = result, verification, backup, upgrade, stats, err
```

`WriteBatch` 将整批数据一次追加到 WAL，再按顺序更新 MemTable；WAL 恢复仍按单条记录重放，因此它不是支持回滚的跨记录事务。

后台 Compaction 使用层级阈值增量合并。L0 达到 `CompactionThreshold` 后合并全部 L0 及其与 L1 重叠的文件；L1 以上每次选择一个源文件和下一层重叠文件。墓碑、`Retention` 和 `MaxSizeBytes` 只在最底层回收，避免旧值重新出现。`Compact()` 保留为显式全量整理入口。

## QueryFormat

[`pkg/parse`](./pkg/parse) 使用 Participle 解析：

```text
matcher{label=value,...}[range] offset duration
```

示例：

```text
error{app=nginx}[5m]
"upstream connection failed"{app=nginx,level="ERROR"}[5m] offset 1h
```

- `matcher` 必填，可以是标识符、数字或带引号字符串。
- 标签只支持等值匹配，标签名不能重复；`{}` 表示不限制标签。
- `range` 必须大于 0，格式遵循 `time.ParseDuration`。
- `offset` 可选，用于把整个查询窗口向过去平移。
- HTTP 查询先使用时间和标签索引缩小候选集，再对日志内容执行 matcher 子串过滤。

```go
query, err := parse.ParseQueryFormat(
    `"upstream connection failed"{app=nginx,level=ERROR}[5m] offset 1h`,
)
if err != nil {
    return err
}

start, end := query.TimeRange(time.Now().UTC())
```

## 配置

| `store.Options` 字段 | 默认值 | 说明 |
| --- | ---: | --- |
| `MemTableLimit` | 4 MiB | Active MemTable 近似字节阈值，`0` 表示不自动切换 |
| `AutoCheckpoint` | `true` | 达到阈值后切换 Immutable MemTable 并在后台刷盘 |
| `CompactionThreshold` | `4` | L0 文件触发合并的数量，`0` 表示关闭 L0 自动触发 |
| `MaxLevels` | `4` | LSM 总层数，至少为 2 |
| `LevelBaseSizeBytes` | 64 MiB | L1 向 L2 下推的容量阈值 |
| `LevelSizeMultiplier` | `10` | 相邻非零层容量倍率 |
| `Retention` | `0` | 最底层合并时的日志保留时长，`0` 表示永久保留 |
| `MaxSizeBytes` | `0` | 最底层合并后的近似数据上限，`0` 表示不限制 |
| `BlockCacheBytes` | 64 MiB | 共享 SSTable Block Cache 容量，`0` 表示禁用 |
| `WALSyncPolicy` | `interval` | `interval` 或 `every-write` |
| `WALSyncInterval` | `50ms` | 周期同步间隔 |

服务从 `.env` 和同名进程环境变量读取配置，进程环境变量优先。`Retention` 在 `.env` 中使用小时数，`WALSyncInterval` 使用 Go duration：

```dotenv
dir=./logs
Address=0.0.0.0
Port=9999
MemTableLimit=4194304
AutoCheckpoint=true
CompactionThreshold=4
MaxLevels=4
LevelBaseSizeBytes=67108864
LevelSizeMultiplier=10
Retention=168
MaxSizeBytes=0
BlockCacheBytes=67108864
WALSyncPolicy=interval
WALSyncInterval=50ms
```

## 存储与恢复

### 日志 Key 与 Value

结构化日志 key：

```text
[8 bytes ordered timestamp][sorted label_name=label_value][0x00][8 bytes sequence]
```

时间戳是翻转符号位后的 big-endian `int64`，标签按名称和值排序，并对 `%`、`|`、`=` 转义。Value 不重复保存标签：

```text
[version][compression][timestamp][message length][compressed message]
```

当前支持原文和 Gzip，默认使用 Gzip。

### SSTable 与 Block 校验

```text
[DataBlock 1 + CRC32C] ... [DataBlock N + CRC32C]
[MetaBlock + CRC32C]
[IndexBlock + CRC32C]
[Footer]
```

Footer 前 6 字节是 UTF-8 Magic `流萤`，后续保存格式版本及 MetaBlock/IndexBlock 位置。SSTable v2 为每个 Block 增加 CRC32C；读取损坏 Block 会返回错误。当前代码兼容只读 v1，并拒绝未知的未来版本。

打开 SSTable 时只加载 Footer、MetaBlock 和 IndexBlock。DataBlock 按查询范围读取并进入共享 LRU Block Cache；校验和启动恢复扫描绕过缓存，避免缓存掩盖磁盘损坏。

### Manifest、锁与崩溃恢复

`MANIFEST` 是已发布 SSTable 的权威目录，记录格式版本、文件名、SSTable 版本、层级、key/时间范围、记录数、下一个文件编号和最后日志序列号。WAL 恢复尚未 Checkpoint 的数据，Manifest 恢复已经发布的数据。

- SSTable 发布前崩溃：从旧 WAL 重放记录。
- Manifest 发布后、WAL 裁剪前崩溃：读取层按最新版本合并重复数据。
- Manifest 或 WAL 原子替换中断：尝试使用 `.bak` 恢复。
- WAL 尾部只有半条记录：启动时截断到最后一条完整记录。
- 多进程同时打开：操作系统文件锁使后打开者失败。

备份是经过 Checkpoint 的完整本地快照，不是增量备份。恢复必须写入新目录。升级只支持向当前格式前进，不提供降级。

## 测试与压测

```bash
go test ./...
go vet ./...
go test -race ./pkg/store ./pkg/wal ./pkg/skipList .
```

基准测试：

```bash
go test ./pkg/store -run '^$' -bench . -benchmem
```

压力工具支持普通 KV 和结构化日志，并在 Checkpoint 后验证读取：

```bash
go run ./cmd/samkv-stress \
  -mode kv -count 100000 -concurrency 8 -value-bytes 256

go run ./cmd/samkv-stress \
  -mode logs -count 100000 -concurrency 8 -value-bytes 256

go run ./cmd/samkv-stress \
  -mode kv -count 10000 -concurrency 4 -strict
```

`-strict` 启用每写 fsync。默认使用临时目录；要保留数据可用 `-dir` 指定一个不存在或为空的目录。

## 当前边界

SamKv 当前是单节点、本地文件系统存储，适合作为嵌入式 KV、单机日志存储和继续开发的基础，但不是生产级分布式日志数据库。

- 没有分片、副本、一致性协议、远程对象存储或跨节点故障转移。
- HTTP 服务没有认证、授权、TLS、租户隔离和请求级限流。
- QueryFormat 目前支持标签等值和内容子串匹配，不支持正则、全文倒排索引、聚合或查询计划。
- 分层 Compaction 是基础增量实现，还没有并行子任务、I/O 限速、写停顿控制和 SSD/HDD 分层。
- 修复工具能检测并隔离损坏文件，但无法重建其中已经丢失的记录。
- 指标是进程内状态；备份是本地全量快照，尚无远程增量备份、PITR 和自动恢复演练。
- 格式已具备显式版本和 v1/v2 兼容读取，但仍需要长期兼容矩阵、模糊测试和跨版本升级测试。