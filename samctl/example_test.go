package main

// 本文件演示 samctl 客户端如何安全拼接裸 IPv6 地址。

import (
	"fmt"
	"time"
)

// ExampleNewClient 展示客户端构造参数；地址不应包含 http:// 或端口。
func ExampleNewClient() {
	client, err := NewClient("::1", 9999, 5*time.Second)
	fmt.Println(client.baseURL, err)
	// Output:
	// http://[::1]:9999 <nil>
}
