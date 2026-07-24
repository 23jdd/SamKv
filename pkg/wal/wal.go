package wal

import (
	"errors"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	DefaultSize   = 4 * 1024 // 4 KiB
	FlushInterval = 50 * time.Millisecond
)

type WalWriter struct {
	file *os.File
}

// WalManger 管理 WAL 的内存缓冲、顺序写入和后台刷盘。
type WalManger struct {
	Dir          string
	options      Options
	buffer       []byte
	flushBatch   []byte
	activeWriter *WalWriter
	writeMu      sync.Mutex
	bufmu        sync.Mutex
	flushCond    sync.Cond
	closed       bool
	flushErr     error
	done         chan struct{}
	closeOnce    sync.Once
	backgroundWG sync.WaitGroup
}

// New 使用默认选项打开或创建 wal.log。
func New(dir string) (*WalManger, error) {
	return NewWithOptions(dir, DefaultOptions())
}

// NewWithOptions 打开或创建 wal.log，并按给定持久性策略启动后台任务。
func NewWithOptions(dir string, options Options) (*WalManger, error) {
	if err := validateOptions(options); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "wal.log")
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		// Windows 替换 WAL 时可能在崩溃点只留下备份文件。
		_ = os.Rename(path+".bak", path)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, err
	}

	wm := &WalManger{
		Dir:          dir,
		options:      options,
		buffer:       make([]byte, 0, options.BufferSize),
		flushBatch:   make([]byte, 0, options.BufferSize),
		activeWriter: &WalWriter{file: file},
		done:         make(chan struct{}),
	}
	wm.flushCond = *sync.NewCond(&wm.bufmu)
	wm.backgroundWG.Add(1)
	go wm.Background()
	return wm, nil
}

// AppendLog 将已经编码的 WAL 数据追加到缓冲区。
// 大于默认缓冲容量的单条数据会先刷出旧缓冲，再直接写盘，避免永久等待。
func (wm *WalManger) AppendLog(data []byte) error {
	wm.bufmu.Lock()
	if wm.closed {
		wm.bufmu.Unlock()
		return os.ErrClosed
	}
	if wm.flushErr != nil {
		err := wm.flushErr
		wm.bufmu.Unlock()
		return err
	}
	wm.bufmu.Unlock()

	if len(data) > wm.options.BufferSize {
		wm.writeMu.Lock()
		defer wm.writeMu.Unlock()

		wm.bufmu.Lock()
		closed := wm.closed
		flushErr := wm.flushErr
		wm.bufmu.Unlock()
		if closed {
			return os.ErrClosed
		}
		if flushErr != nil {
			return flushErr
		}
		if err := wm.flushBufferLocked(); err != nil {
			return err
		}
		return wm.writeAndSyncLocked(data)
	}

	wm.bufmu.Lock()
	defer wm.bufmu.Unlock()
	for len(wm.buffer)+len(data) > cap(wm.buffer) && !wm.closed && wm.flushErr == nil {
		wm.flushCond.Wait()
	}
	if wm.closed {
		return os.ErrClosed
	}
	if wm.flushErr != nil {
		return wm.flushErr
	}
	wm.buffer = append(wm.buffer, data...)
	return nil
}

func (wm *WalManger) triggerFlush() error {
	wm.writeMu.Lock()
	defer wm.writeMu.Unlock()
	return wm.flushBufferLocked()
}

// flushBufferLocked 交换活动缓冲和刷盘缓冲。
// 调用方必须持有 writeMu；写盘失败的数据会放回活动缓冲，避免被下一批覆盖。
func (wm *WalManger) flushBufferLocked() error {
	wm.bufmu.Lock()
	if len(wm.buffer) == 0 {
		wm.bufmu.Unlock()
		return nil
	}
	reusable := wm.flushBatch
	wm.flushBatch = wm.buffer
	wm.buffer = reusable[:0]
	wm.flushCond.Broadcast()
	wm.bufmu.Unlock()

	if err := wm.writeAndSyncLocked(wm.flushBatch); err != nil {
		wm.bufmu.Lock()
		failed := wm.flushBatch
		restored := make([]byte, 0, len(failed)+len(wm.buffer))
		restored = append(restored, failed...)
		restored = append(restored, wm.buffer...)
		wm.buffer = restored
		wm.flushBatch = reusable[:0]
		wm.flushErr = err
		wm.flushCond.Broadcast()
		wm.bufmu.Unlock()
		return err
	}

	wm.bufmu.Lock()
	wm.flushErr = nil
	wm.bufmu.Unlock()
	return nil
}

func (wm *WalManger) writeAndSyncLocked(data []byte) error {
	for len(data) > 0 {
		n, err := wm.activeWriter.file.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return os.ErrInvalid
		}
		data = data[n:]
	}
	return wm.activeWriter.file.Sync()
}

// Background 定时把 WAL 缓冲同步到磁盘。
func (wm *WalManger) Background() {
	defer wm.backgroundWG.Done()

	interval := wm.options.SyncInterval
	if interval <= 0 {
		interval = FlushInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := wm.triggerFlush(); err != nil {
				log.Println("[WAL] flush error:", err)
			}
		case <-wm.done:
			return
		}
	}
}

// Err 返回最近一次后台刷盘错误；后续刷盘成功后会清除该错误。
func (wm *WalManger) Err() error {
	wm.bufmu.Lock()
	defer wm.bufmu.Unlock()
	return wm.flushErr
}

// AppendRecord 编码并追加一条 WAL 记录。
func (wm *WalManger) AppendRecord(record *Record) error {
	data, err := record.Encode()
	if err != nil {
		return err
	}
	return wm.AppendLog(data)
}
