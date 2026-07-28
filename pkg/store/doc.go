// Package store 提供一个可嵌入进程的并发安全 LSM 日志存储引擎。
//
// 推荐从 DefaultOptions 创建配置，再用 NewStoreManagerWithOptions 打开独占数据目录。
// WriteLog 把日志数据先写入 WAL 再进入 MemTable，Checkpoint 把内存数据发布到 SSTable，
// CompactLevel 做增量分层合并。Query 提供按时间和标签过滤的结构化日志检索。
//
// Store 不是分布式数据库：目录锁仅保护单个数据目录，调用方仍需自行处理跨机器复制、认证和备份调度。
package store
