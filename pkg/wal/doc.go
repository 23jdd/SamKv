// Package wal 实现 SamKV 的顺序预写日志、记录校验和可配置持久性策略。
//
// 正常写入使用 PutRecord/DeleteRecord 构造记录，再交给 WalManger.AppendRecord。
// SyncEveryWrite 在返回前执行文件 Sync，适合不能接受最近写入丢失的场景；SyncInterval
// 先写入内存缓冲，由周期任务或缓冲满触发 Sync，吞吐更高，但进程或主机崩溃时可能
// 丢失最近一个同步窗口。
//
// 单条编码记录可以大于 BufferSize：管理器会先刷出旧缓冲，再直接写入并 Sync，不会等待
// 一个永远无法容纳它的缓冲。Flush 可建立显式持久化边界，Replace 用于 Checkpoint 后
// 原子改写仍需恢复的记录，Close 会停止后台任务并尝试刷出剩余数据。
//
// ReadRecord 校验长度、类型和 CRC32，最大接受 64 MiB payload。io.EOF 表示正常读到文件
// 末尾，io.ErrUnexpectedEOF 表示尾部记录不完整；ErrChecksum 表示完整长度内的数据损坏。
// WalManger 可并发追加，但同一数据目录只能由上层 Store 的目录锁保证单进程独占。
package wal
