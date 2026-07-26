// Command samkv-admin 提供数据目录校验、离线修复、备份恢复和格式升级。
//
// 修改数据的 repair、restore 与 upgrade 应先在副本上演练，并确保在线服务不再持有目录锁。
package main
