// Command samkv-stress 对新建或空数据目录运行可复现的日志压力负载。
//
// 使用 -strict 比较每次写入 fsync 的持久性成本，使用 -payload-pattern random 降低压缩率带来的吞吐偏差。
package main
