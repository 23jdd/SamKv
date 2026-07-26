// Command SamKV 启动一个嵌入式 LSM Store 的 HTTP 服务。
//
// 直接运行会监听 Address/Port，并使用 dir 指定的数据目录。
// start、stop、status 提供轻量后台进程控制；长期部署建议交给操作系统服务管理器。
package main
