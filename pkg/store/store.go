package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/23jdd/SamKv/pkg/wal"
)

// StoreManger 管理活动/只读 MemTable、WAL、SSTable 和后台维护任务。
// 名称保留了早期版本的拼写，NewStoreManager 可作为正确拼写的入口。
// StoreManager 是 StoreManger 的正确拼写别名。
type StoreManager = StoreManger

type StoreManger struct {
	mu            sync.RWMutex
	maintenanceMu sync.Mutex

	dir        string
	mem        *MemTable
	immutables []*MemTable
	wm         *wal.WalManger
	options    Options

	sstables      []*SStable
	nextSSTableID uint64
	manifest      Manifest
	sequence      atomic.Uint64

	flushCh       chan struct{}
	compactionCh  chan struct{}
	done          chan struct{}
	workerWG      sync.WaitGroup
	closeOnce     sync.Once
	closeErr      error
	closed        bool
	backgroundErr error
	now           func() time.Time
}

// NewStoreManger 使用兼容旧 API 的方式创建 Store。
// limit 是 MemTable 阈值；达到阈值后会自动切换为 Immutable MemTable 并后台刷盘。
func NewStoreManger(dir string, limit int) (*StoreManger, error) {
	options := DefaultOptions()
	options.MemTableLimit = limit
	return NewStoreMangerWithOptions(dir, options)
}

// NewStoreManager 是 NewStoreManger 的正确拼写别名。
func NewStoreManager(dir string, limit int) (*StoreManger, error) {
	return NewStoreManger(dir, limit)
}

// NewStoreManagerWithOptions 使用 Options 创建 Store。
func NewStoreManagerWithOptions(dir string, options Options) (*StoreManager, error) {
	return NewStoreMangerWithOptions(dir, options)
}

// NewStoreMangerWithOptions 创建 Store，加载 SSTable，回放 WAL 并启动后台维护协程。
func NewStoreMangerWithOptions(dir string, options Options) (*StoreManger, error) {
	if err := validateOptions(options); err != nil {
		return nil, err
	}
	wm, err := wal.New(dir)
	if err != nil {
		return nil, err
	}

	st := &StoreManger{
		dir:           dir,
		mem:           NewMemTable(options.MemTableLimit),
		wm:            wm,
		options:       options,
		nextSSTableID: 1,
		manifest:      newManifest(),
		flushCh:       make(chan struct{}, 1),
		compactionCh:  make(chan struct{}, 1),
		done:          make(chan struct{}),
		now:           time.Now,
	}
	if err := st.loadSSTables(); err != nil {
		st.closeSSTablesLocked()
		_ = wm.Close()
		return nil, err
	}
	if err := RecoverWALFile(filepath.Join(dir, "wal.log"), st.mem); err != nil {
		st.closeSSTablesLocked()
		_ = wm.Close()
		return nil, err
	}
	st.sequence.Store(st.manifest.LastSequence)
	if err := st.restoreSequence(); err != nil {
		st.closeSSTablesLocked()
		_ = wm.Close()
		return nil, err
	}

	st.workerWG.Add(1)
	go st.runMaintenance()

	st.mu.Lock()
	if st.options.AutoCheckpoint && st.mem.ShouldFlush() {
		st.freezeActiveLocked()
		st.scheduleFlushLocked()
	}
	st.mu.Unlock()
	return st, nil
}

// Put 先写 WAL，再写活动 MemTable。
func (st *StoreManger) Put(key string, val string) error {
	st.mu.Lock()
	defer st.mu.Unlock()

	if err := st.checkWritableLocked(); err != nil {
		return err
	}
	if err := st.wm.AppendRecord(wal.PutRecord([]byte(key), []byte(val))); err != nil {
		return err
	}
	if err := st.mem.Put(key, val); err != nil {
		return err
	}
	st.maybeFreezeLocked()
	return nil
}

// Get 按“活动表 -> 新只读表 -> 旧只读表 -> 新 SSTable -> 旧 SSTable”的顺序查询。
func (st *StoreManger) Get(key string) (string, bool) {
	st.mu.RLock()
	defer st.mu.RUnlock()

	if entry, ok := st.mem.GetEntry(key); ok {
		if entry.Deleted {
			return "", false
		}
		return entry.Value, true
	}
	for i := len(st.immutables) - 1; i >= 0; i-- {
		if entry, ok := st.immutables[i].GetEntry(key); ok {
			if entry.Deleted {
				return "", false
			}
			return entry.Value, true
		}
	}
	for i := len(st.sstables) - 1; i >= 0; i-- {
		record, ok, err := st.sstables[i].GetRecord(key)
		if err != nil || !ok {
			continue
		}
		if record.Deleted {
			return "", false
		}
		return record.Val, true
	}
	return "", false
}

// Delete 写入墓碑，用于覆盖所有旧层级中的同名 key。
func (st *StoreManger) Delete(key string) error {
	st.mu.Lock()
	defer st.mu.Unlock()

	if err := st.checkWritableLocked(); err != nil {
		return err
	}
	if err := st.wm.AppendRecord(wal.DeleteRecord([]byte(key))); err != nil {
		return err
	}
	if err := st.mem.Delete(key); err != nil {
		return err
	}
	st.maybeFreezeLocked()
	return nil
}

// BackgroundError 返回最近一次后台刷盘或 Compaction 错误。
func (st *StoreManger) BackgroundError() error {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.backgroundErr
}

func (st *StoreManger) checkWritableLocked() error {
	if st.closed {
		return ErrStoreClosed
	}
	if st.backgroundErr != nil {
		return errors.Join(ErrBackgroundFailure, st.backgroundErr)
	}
	return nil
}

func (st *StoreManger) maybeFreezeLocked() {
	if !st.options.AutoCheckpoint || !st.mem.ShouldFlush() {
		return
	}
	st.freezeActiveLocked()
	st.scheduleFlushLocked()
}

func (st *StoreManger) freezeActiveLocked() bool {
	if st.mem.Len() == 0 {
		return false
	}
	st.mem.MarkImmutable()
	st.immutables = append(st.immutables, st.mem)
	st.mem = NewMemTable(st.options.MemTableLimit)
	return true
}

func (st *StoreManger) scheduleFlushLocked() {
	select {
	case st.flushCh <- struct{}{}:
	default:
	}
}

func (st *StoreManger) scheduleCompactionLocked() {
	if st.options.CompactionThreshold <= 0 || len(st.sstables) < st.options.CompactionThreshold {
		return
	}
	select {
	case st.compactionCh <- struct{}{}:
	default:
	}
}

// Close 停止后台任务，然后关闭 WAL 和 SSTable 文件句柄。
func (st *StoreManger) Close() error {
	st.closeOnce.Do(func() {
		st.mu.Lock()
		st.closed = true
		close(st.done)
		st.mu.Unlock()

		st.workerWG.Wait()
		st.maintenanceMu.Lock()
		defer st.maintenanceMu.Unlock()

		st.mu.Lock()
		defer st.mu.Unlock()
		st.closeErr = errors.Join(st.closeSSTablesLocked(), st.wm.Close())
	})
	return st.closeErr
}

func (st *StoreManger) closeSSTablesLocked() error {
	var closeErr error
	for _, table := range st.sstables {
		closeErr = errors.Join(closeErr, table.Close())
	}
	return closeErr
}

// Checkpoint 冻结当前活动 MemTable，并同步等待所有 Immutable MemTable 完成刷盘。
func (st *StoreManger) Checkpoint() (string, error) {
	st.mu.Lock()
	if st.closed {
		st.mu.Unlock()
		return "", ErrStoreClosed
	}
	st.freezeActiveLocked()
	st.mu.Unlock()

	path, err := st.flushAllImmutables()
	if err != nil {
		st.setBackgroundError(err)
		return path, err
	}
	if path == "" {
		err = st.wm.Flush()
	}
	if err == nil {
		st.clearBackgroundError()
	}
	return path, err
}

func (st *StoreManger) flushAllImmutables() (string, error) {
	st.maintenanceMu.Lock()
	defer st.maintenanceMu.Unlock()

	var lastPath string
	for {
		path, flushed, err := st.flushOldestImmutable()
		if err != nil {
			return lastPath, err
		}
		if !flushed {
			return lastPath, nil
		}
		lastPath = path
	}
}

func (st *StoreManger) flushOldestImmutable() (string, bool, error) {
	st.mu.RLock()
	if len(st.immutables) == 0 {
		st.mu.RUnlock()
		return "", false, nil
	}
	immutable := st.immutables[0]
	path := st.nextSSTablePathLocked()
	st.mu.RUnlock()

	if err := st.wm.Flush(); err != nil {
		return "", false, err
	}
	table, err := WriteSStable(path, immutable.Flush())
	if err != nil {
		return "", false, err
	}

	st.mu.Lock()
	defer st.mu.Unlock()
	if len(st.immutables) == 0 || st.immutables[0] != immutable {
		_ = table.Close()
		_ = os.Remove(path)
		return "", false, errors.New("store: immutable memtable changed during flush")
	}

	nextManifest := st.manifest
	nextManifest.SSTables = append([]ManifestSSTable(nil), st.manifest.SSTables...)
	nextManifest.SSTables = append(nextManifest.SSTables, manifestEntryFromSSTable(path, table))
	nextManifest.NextFileID = st.nextSSTableID + 1
	nextManifest.LastSequence = st.sequence.Load()
	if err := saveManifest(st.dir, nextManifest); err != nil {
		_ = table.Close()
		_ = os.Remove(path)
		return "", false, err
	}

	st.sstables = append(st.sstables, table)
	st.nextSSTableID++
	st.manifest = nextManifest
	st.immutables = st.immutables[1:]

	walSnapshot, err := st.encodeWALSnapshotLocked()
	if err != nil {
		return path, true, err
	}
	if err := st.wm.Replace(walSnapshot); err != nil {
		return path, true, err
	}
	st.scheduleCompactionLocked()
	return path, true, nil
}

func (st *StoreManger) encodeWALSnapshotLocked() ([]byte, error) {
	var data []byte
	appendRecords := func(records []Record) error {
		for _, record := range records {
			var walRecord *wal.Record
			if record.Deleted {
				walRecord = wal.DeleteRecord([]byte(record.Key))
			} else {
				walRecord = wal.PutRecord([]byte(record.Key), []byte(record.Val))
			}
			encoded, err := walRecord.Encode()
			if err != nil {
				return err
			}
			data = append(data, encoded...)
		}
		return nil
	}

	for _, immutable := range st.immutables {
		if err := appendRecords(immutable.Entries()); err != nil {
			return nil, err
		}
	}
	if err := appendRecords(st.mem.Entries()); err != nil {
		return nil, err
	}
	return data, nil
}

// ReLoad 清空内存表并重新回放 wal.log。
// 正常启动不需要手动调用；NewStoreMangerWithOptions 已自动恢复。
func (st *StoreManger) ReLoad() error {
	st.maintenanceMu.Lock()
	defer st.maintenanceMu.Unlock()

	st.mu.Lock()
	defer st.mu.Unlock()
	if st.closed {
		return ErrStoreClosed
	}
	if err := st.wm.Flush(); err != nil {
		return err
	}
	st.mem = NewMemTable(st.options.MemTableLimit)
	st.immutables = nil
	if err := RecoverWALFile(filepath.Join(st.dir, "wal.log"), st.mem); err != nil {
		return err
	}
	return st.restoreSequence()
}

func (st *StoreManger) runMaintenance() {
	defer st.workerWG.Done()
	for {
		select {
		case <-st.flushCh:
			_, err := st.flushAllImmutables()
			if err != nil {
				st.setBackgroundError(err)
			} else {
				st.clearBackgroundError()
			}
		case <-st.compactionCh:
			_, err := st.Compact()
			if err != nil && !errors.Is(err, ErrStoreClosed) {
				st.setBackgroundError(err)
			} else if err == nil {
				st.clearBackgroundError()
			}
		case <-st.done:
			return
		}
	}
}

func (st *StoreManger) setBackgroundError(err error) {
	if err == nil {
		return
	}
	st.mu.Lock()
	st.backgroundErr = err
	st.mu.Unlock()
}

func (st *StoreManger) clearBackgroundError() {
	st.mu.Lock()
	st.backgroundErr = nil
	st.mu.Unlock()
}

func (st *StoreManger) loadSSTables() error {
	manifest, ok, err := loadManifest(st.dir)
	if err != nil {
		return err
	}
	if ok {
		return st.loadSSTablesFromManifest(manifest)
	}

	paths, err := filepath.Glob(filepath.Join(st.dir, "*.sst"))
	if err != nil {
		return err
	}
	sort.Strings(paths)

	var maxID uint64
	for _, path := range paths {
		table, err := OpenSStable(path)
		if err != nil {
			st.closeSSTablesLocked()
			st.sstables = nil
			return err
		}
		st.sstables = append(st.sstables, table)
		id, valid := sstableID(path)
		if valid && id > maxID {
			maxID = id
		}
	}
	if maxID >= st.nextSSTableID {
		st.nextSSTableID = maxID + 1
	}
	st.manifest = manifestFromSSTables(st.nextSSTableID, paths, st.sstables)
	if len(st.sstables) > 0 {
		return saveManifest(st.dir, st.manifest)
	}
	return nil
}

// loadSSTablesFromManifest 按 Manifest 顺序加载 SSTable；后面的文件拥有更高覆盖优先级。
func (st *StoreManger) loadSSTablesFromManifest(manifest Manifest) error {
	st.manifest = manifest
	st.nextSSTableID = manifest.NextFileID

	var maxID uint64
	for _, entry := range manifest.SSTables {
		path := filepath.Join(st.dir, entry.File)
		table, err := OpenSStable(path)
		if err != nil {
			st.closeSSTablesLocked()
			st.sstables = nil
			return err
		}
		st.sstables = append(st.sstables, table)
		id, valid := sstableID(path)
		if valid && id > maxID {
			maxID = id
		}
	}
	if st.nextSSTableID == 0 {
		st.nextSSTableID = 1
	}
	if maxID >= st.nextSSTableID {
		st.nextSSTableID = maxID + 1
		st.manifest.NextFileID = st.nextSSTableID
	}
	return nil
}

// manifestFromSSTables 用于兼容没有 MANIFEST 的旧目录。
func manifestFromSSTables(nextFileID uint64, paths []string, tables []*SStable) Manifest {
	manifest := Manifest{NextFileID: nextFileID, SSTables: make([]ManifestSSTable, 0, len(tables))}
	for i, table := range tables {
		manifest.SSTables = append(manifest.SSTables, manifestEntryFromSSTable(paths[i], table))
	}
	return manifest
}

func (st *StoreManger) nextSSTablePathLocked() string {
	return filepath.Join(st.dir, fmt.Sprintf("%020d.sst", st.nextSSTableID))
}

func sstableID(path string) (uint64, bool) {
	base := filepath.Base(path)
	if !strings.HasSuffix(base, ".sst") {
		return 0, false
	}
	id, err := strconv.ParseUint(strings.TrimSuffix(base, ".sst"), 10, 64)
	if err != nil {
		return 0, false
	}
	return id, true
}
