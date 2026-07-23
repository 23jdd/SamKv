package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/23jdd/SamKv/pkg/store"
)

const (
	defaultDataDir       = "./data"
	defaultServerAddress = "0.0.0.0"
	defaultServerPort    = 9999
	shutdownTimeout      = 10 * time.Second
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() (returnErr error) {
	options := Load()
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
