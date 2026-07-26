// Package store 提供一个可嵌入进程的并发安全 LSM KV/日志存储引擎。
//
// 推荐从 DefaultOptions 创建配置，再用 NewStoreManagerWithOptions 打开独占数据目录。
// Put/Delete 先进入 WAL，Checkpoint 把内存数据发布到 SSTable，CompactLevel 做增量分层合并。
// WriteLog 与 Query 在同一 KV 内核上提供按时间和标签过滤的结构化日志接口。
//
// Store 不是分布式数据库：目录锁仅保护单个数据目录，调用方仍需自行处理跨机器复制、认证和备份调度。
package store
