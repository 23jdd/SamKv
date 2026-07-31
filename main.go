package main

// 本文件是 SamKV 服务入口，负责命令解析、Store 生命周期、HTTP 监听和信号优雅关闭。
// start/stop/status 是进程管理子命令；普通启动从环境读取数据目录、地址、端口和存储配置。

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/23jdd/SamKv/pkg/store"
	"github.com/mbndr/figlet4go"
)

const (
	defaultDataDir       = "./data"
	defaultServerAddress = "0.0.0.0"
	defaultServerPort    = 9999
	shutdownTimeout      = 10 * time.Second
)

type serverConfig struct {
	envFile  string
	isdaemon bool
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

// run 启动服务并阻塞到监听失败或收到 SIGINT/SIGTERM；关闭时合并 HTTP 与 Store 的错误。
func run(args []string) (returnErr error) {
	if len(args) >= 1 {
		switch args[0] {
		case "start":
			err := startDaemon(args[1:]...)
			if err != nil {
				panic(err)
			}
			return
		case "stop":
			err := stopDaemon()
			if err != nil {
				panic(err)
			}
			return
		case "status":
			status()
			return
		}
	}
	config, err := parseServerConfig(args)
	if err != nil {
		return err
	}
	if config.isdaemon {
		err := startDaemon()
		if err != nil {
			panic(err)
		}
		return
	}
	options := LoadEnvFile(config.envFile)
	dir := os.Getenv("dir")
	if dir == "" {
		dir = defaultDataDir
	}

	database, err := store.NewStoreManagerWithOptions(dir, options)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, database.Close())
	}()

	address, port, err := loadServerAddress()
	if err != nil {
		return err
	}
	ascii := figlet4go.NewAsciiRender()
	renderStr, _ := ascii.Render("SamKv")
	fmt.Println(renderStr)
	server := NewServer(port, address, database)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- server.Run()
	}()
	log.Printf("SamKV HTTP server listening on http://%s", server.Addr())

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	shutdownErr := server.Shutdown(shutdownCtx)
	if shutdownErr != nil {
		shutdownErr = errors.Join(shutdownErr, server.Close())
	}
	return errors.Join(shutdownErr, <-serveErr)
}

func parseServerConfig(args []string) (serverConfig, error) {
	config := serverConfig{envFile: ".env", isdaemon: false}
	flags := flag.NewFlagSet("samkv", flag.ContinueOnError)
	flags.StringVar(&config.envFile, "f", config.envFile, ".env file path")
	flags.BoolVar(&config.isdaemon, "d", config.isdaemon, "judge whether daemon process")
	if err := flags.Parse(args); err != nil {
		return serverConfig{}, err
	}
	if flags.NArg() != 0 {
		return serverConfig{}, fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	return config, nil
}

// loadServerAddress 读取大小写敏感的 Address/Port；服务入口不接受端口 0，合法范围为 1..65535。
func loadServerAddress() (string, int, error) {
	address := os.Getenv("Address")
	if address == "" {
		address = defaultServerAddress
	}

	port := defaultServerPort
	if rawPort := os.Getenv("Port"); rawPort != "" {
		parsed, err := strconv.Atoi(rawPort)
		if err != nil || parsed < 1 || parsed > 65535 {
			return "", 0, fmt.Errorf("invalid Port %q", rawPort)
		}
		port = parsed
	}
	return address, port, nil
}
