package wal

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	DefaultSize   = 4 * 1024 // 4kb
	FlushInterval = 50 * time.Millisecond
)

// Example

type WalWriter struct {
	file *os.File
}
type WalManger struct {
	Dir          string
	buffer       []byte
	flushBatch   []byte
	activeWriter *WalWriter
	bufmu        sync.Mutex
	flushCond    sync.Cond
	closed       bool
}

// seq
func New(dir string) (*WalManger, error) {
	err := os.MkdirAll(dir, 0644)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "wal.log")
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_RDONLY, 0644)
	if err != nil {
		return nil, err
	}
	w := &WalWriter{file: file}
	wm := &WalManger{}
	wm.activeWriter = w
	wm.buffer = make([]byte, 0, DefaultSize)
	wm.flushBatch = make([]byte, 0, DefaultSize)
	wm.Dir = dir
	wm.flushCond = *sync.NewCond(&wm.bufmu)
	go wm.Background()
	return wm, nil
}

func (wm *WalManger) AppendLog(data []byte) error {
	wm.bufmu.Lock()
	defer wm.bufmu.Unlock()
	for len(wm.buffer)+len(data) > cap(wm.buffer) && !wm.closed {
		wm.flushCond.Wait()
	}
	if wm.closed {
		return os.ErrClosed
	}
	wm.buffer = append(wm.buffer, data...)
	return nil
}
func (wm *WalManger) triggerFlush() {
	wm.bufmu.Lock()
	if len(wm.buffer) == 0 {
		wm.bufmu.Unlock()
		return
	}
	batch := wm.flushBatch
	wm.flushBatch = wm.buffer
	wm.buffer = batch[:0]
	wm.flushCond.Broadcast()
	wm.bufmu.Unlock()
	_, err := wm.activeWriter.file.Write(wm.flushBatch)
	if err == nil {
		err = wm.activeWriter.file.Sync()
	}
	if err != nil {
		log.Println("[WAL] Error", err)
	}
	fmt.Println("Sync")

}

// flusher
func (wm *WalManger) Background() {
	ticker := time.NewTicker(FlushInterval)
	for {
		<-ticker.C
		wm.triggerFlush()
	}
}

func (wm *WalManger) AppendRecord(r *Record) error {
	data, err := r.Encode()
	if err != nil {
		return err
	}
	return wm.AppendLog(data)
}
