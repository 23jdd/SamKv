package main

// 本文件用标准库 http.Server 承载 Gin 路由，并设置请求头、读写和空闲超时。
// Run 会阻塞；正常停止应先调用 Shutdown，超时后再用 Close 强制断开连接。

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	serverReadHeaderTimeout = 5 * time.Second
	serverReadTimeout       = 30 * time.Second
	serverWriteTimeout      = 30 * time.Second
	serverIdleTimeout       = 60 * time.Second
)

// Server 封装 Gin Handler 和标准库 http.Server 的生命周期。
type Server struct {
	port       int
	address    string
	httpServer *http.Server
}

// NewServer 创建一个使用 database 处理日志请求的 HTTP Server。
// port 可为 0 但 Run 不会回填系统分配端口；负数或大于 65535 会 panic，database 为 nil 也会 panic。
func NewServer(port int, address string, database logRouterStore) *Server {
	gin.SetMode(gin.ReleaseMode)
	if port < 0 || port > 65535 {
		panic("server: port must be between 0 and 65535")
	}

	handler := NewRouter(database)
	server := &Server{port: port, address: address}
	server.httpServer = &http.Server{
		Addr:              server.Addr(),
		Handler:           handler,
		ReadHeaderTimeout: serverReadHeaderTimeout,
		ReadTimeout:       serverReadTimeout,
		WriteTimeout:      serverWriteTimeout,
		IdleTimeout:       serverIdleTimeout,
		MaxHeaderBytes:    1 << 20,
	}
	return server
}

// Addr 返回 Server 监听的 host:port 地址，并正确处理 IPv6 地址。
func (s *Server) Addr() string {
	return net.JoinHostPort(s.address, strconv.Itoa(s.port))
}

// Handler 返回 HTTP Handler，供测试或嵌入其他 HTTP Server 使用。
func (s *Server) Handler() http.Handler {
	return s.httpServer.Handler
}

// Run 启动 HTTP 服务并阻塞到服务停止。
func (s *Server) Run() error {
	err := s.httpServer.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Shutdown 停止接收新请求，并等待处理中的请求在 ctx 到期前完成。
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

// Close 立即关闭所有活动连接，通常只在优雅关闭超时时使用。
func (s *Server) Close() error {
	return s.httpServer.Close()
}
