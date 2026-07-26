package store

// 本文件组合 MemTable、WAL、SSTable、Manifest 和后台任务，提供 Store 的生命周期及普通 KV API。
// 同一实例可供多个 goroutine 使用；调用 Close 后所有读写和维护操作均不应继续使用。

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

	dir               string
	dirLock           *directoryLock
	mem               *MemTable
	immutables        []*MemTable
	wm                *wal.WalManger
	options           Options
	blockCache        *BlockCache
	compactionLimiter byteRateLimiter

	sstables       []*SStable
	nextSSTableID  uint64
	manifest       Manifest
	sequence       atomic.Uint64
	stats          statsCounters
	recoveryReport wal.RecoveryReport

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
	dirLock, err := acquireDirectoryLock(dir)
	if err != nil {
		return nil, err
	}
	walOptions := wal.DefaultOptions()
	walOptions.SyncPolicy = options.WALSyncPolicy
	if options.WALSyncInterval > 0 {
		walOptions.SyncInterval = options.WALSyncInterval
	}
	wm, err := wal.NewWithOptions(dir, walOptions)
	if err != nil {
		_ = dirLock.release()
		return nil, err
	}

	st := &StoreManger{
		dir:               dir,
		dirLock:           dirLock,
		mem:               NewMemTable(options.MemTableLimit),
		wm:                wm,
		options:           options,
		blockCache:        NewBlockCache(options.BlockCacheBytes),
		compactionLimiter: newCompactionRateLimiter(options.CompactionRateLimitBytesPerSec),
		nextSSTableID:     1,
		manifest:          newManifest(),
		flushCh:           make(chan struct{}, 1),
		compactionCh:      make(chan struct{}, 1),
		done:              make(chan struct{}),
		now:               time.Now,
	}
	if err := st.loadSSTables(); err != nil {
		st.closeSSTablesLocked()
		_ = wm.Close()
		_ = dirLock.release()
		return nil, err
	}
	recoveryReport, err := RecoverWALDirectory(dir, st.mem)
	if err != nil {
		st.closeSSTablesLocked()
		_ = wm.Close()
		_ = dirLock.release()
		return nil, err
	}
	st.recoveryReport = recoveryReport
	st.sequence.Store(st.manifest.LastSequence)
	if err := st.restoreSequence(); err != nil {
		st.closeSSTablesLocked()
		_ = wm.Close()
		_ = dirLock.release()
		return nil, err
	}

	st.workerWG.Add(1)
	go st.runMaintenance()

	st.mu.Lock()
	if st.options.AutoCheckpoint && st.mem.ShouldFlush() {
		if _, err := st.freezeActiveLocked(); err != nil {
			st.backgroundErr = err
		} else {
			st.scheduleFlushLocked()
		}
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
	st.stats.writeOperations.Add(1)
	st.maybeFreezeLocked()
	return nil
}

// Get 按“活动表 -> 新只读表 -> 旧只读表 -> 新 SSTable -> 旧 SSTable”的顺序查询。
// 兼容旧 API；发生磁盘读取错误时返回未找到，并通过 BackgroundError 暴露故障。
func (st *StoreManger) Get(key string) (string, bool) {
	value, found, _ := st.GetWithError(key)
	return value, found
}

// GetWithError 返回点查询结果，并保留 SSTable 校验或读取错误。
func (st *StoreManger) GetWithError(key string) (string, bool, error) {
	st.stats.readOperations.Add(1)
	st.mu.RLock()

	if entry, ok := st.mem.GetEntry(key); ok {
		st.mu.RUnlock()
		if entry.Deleted {
			return "", false, nil
		}
		return entry.Value, true, nil
	}
	for i := len(st.immutables) - 1; i >= 0; i-- {
		if entry, ok := st.immutables[i].GetEntry(key); ok {
			st.mu.RUnlock()
			if entry.Deleted {
				return "", false, nil
			}
			return entry.Value, true, nil
		}
	}
	for i := len(st.sstables) - 1; i >= 0; i-- {
		record, ok, err := st.sstables[i].GetRecord(key)
		if err != nil {
			st.mu.RUnlock()
			st.setBackgroundError(err)
			return "", false, err
		}
		if !ok {
			continue
		}
		st.mu.RUnlock()
		if record.Deleted {
			return "", false, nil
		}
		return record.Val, true, nil
	}
	st.mu.RUnlock()
	return "", false, nil
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
	st.stats.writeOperations.Add(1)
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
	if _, err := st.freezeActiveLocked(); err != nil {
		st.backgroundErr = err
		return
	}
	st.scheduleFlushLocked()
}

func (st *StoreManger) freezeActiveLocked() (bool, error) {
	if st.mem.Len() == 0 {
		return false, nil
	}
	sealedSegment, err := st.wm.Rotate()
	if err != nil {
		return false, err
	}
	st.mem.SetWALSegmentCutoff(sealedSegment)
	st.mem.MarkImmutable()
	st.immutables = append(st.immutables, st.mem)
	st.mem = NewMemTable(st.options.MemTableLimit)
	return true, nil
}

func (st *StoreManger) scheduleFlushLocked() {
	select {
	case st.flushCh <- struct{}{}:
	default:
	}
}

func (st *StoreManger) scheduleCompactionLocked() {
	if st.options.CompactionThreshold <= 0 || st.nextCompactionLevelLocked() < 0 {
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
		st.closeErr = errors.Join(st.closeSSTablesLocked(), st.wm.Close(), st.dirLock.release())
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
// 一致性顺序是封存 WAL segment -> 写并发布 SSTable -> 发布 Manifest -> 回收已封存 segment。
// Manifest 发布前 WAL 保持完整；发布后即使旧段尚未删除也只会在恢复时产生可覆盖的重复记录。
// 返回值是最后生成的 SSTable 路径；没有待刷数据时为空，但仍会按 WALSyncPolicy 完成一次 Flush。
func (st *StoreManger) Checkpoint() (string, error) {
	st.mu.Lock()
	if st.closed {
		st.mu.Unlock()
		return "", ErrStoreClosed
	}
	_, freezeErr := st.freezeActiveLocked()
	st.mu.Unlock()
	if freezeErr != nil {
		return "", freezeErr
	}

	path, err := st.flushAllImmutables()
	if err != nil {
		st.setBackgroundError(err)
		return path, err
	}
	if path == "" {
		err = st.wm.Flush()
	}
	if err == nil {
		st.stats.checkpoints.Add(1)
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
	walCutoff := immutable.WALSegmentCutoff()
	path := st.nextSSTablePathLocked()
	st.mu.RUnlock()

	if err := st.wm.Flush(); err != nil {
		return "", false, err
	}
	table, err := WriteSStable(path, immutable.Flush())
	if err != nil {
		return "", false, err
	}
	table.SetBlockCache(st.blockCache)

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

	if _, err := st.wm.PruneThrough(walCutoff); err != nil {
		return path, true, err
	}
	st.scheduleCompactionLocked()
	return path, true, nil
}

// ReLoad 清空内存表并重新回放全部 WAL segment。
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
	recoveryReport, err := RecoverWALDirectory(st.dir, st.mem)
	if err != nil {
		return err
	}
	st.recoveryReport = recoveryReport
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
			_, err := st.CompactNextLevel()
			if err != nil && !errors.Is(err, ErrStoreClosed) {
				st.setBackgroundError(err)
			} else if err == nil {
				st.clearBackgroundError()
				st.mu.Lock()
				st.scheduleCompactionLocked()
				st.mu.Unlock()
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
	if err := cleanupSSTableTemps(st.dir); err != nil {
		return err
	}
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
		table.SetBlockCache(st.blockCache)
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

	maxID, err := maxSSTableIDOnDisk(st.dir)
	if err != nil {
		return err
	}
	for _, entry := range manifest.SSTables {
		path := filepath.Join(st.dir, entry.File)
		table, err := OpenSStable(path)
		if err != nil {
			st.closeSSTablesLocked()
			st.sstables = nil
			return err
		}
		table.SetBlockCache(st.blockCache)
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
	manifest := newManifest()
	manifest.NextFileID = nextFileID
	manifest.SSTables = make([]ManifestSSTable, 0, len(tables))
	for i, table := range tables {
		manifest.SSTables = append(manifest.SSTables, manifestEntryFromSSTable(paths[i], table))
	}
	return manifest
}

// cleanupSSTableTemps 删除写 SSTable 时在原子重命名前遗留的临时文件。
// 只有以 .sst.tmp 结尾的普通文件会被删除，已经发布的 .sst 和其他文件不受影响。
func cleanupSSTableTemps(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	removed := false
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sst.tmp") {
			continue
		}
		if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		removed = true
	}
	if removed {
		return syncStoreDirectory(dir)
	}
	return nil
}

// maxSSTableIDOnDisk 返回目录中所有合法数字文件名 SSTable 的最大编号。
// Manifest 未引用的孤立表不会参与读取，但编号仍需保留，避免后续发布覆盖待修复文件。
func maxSSTableIDOnDisk(dir string) (uint64, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	var maxID uint64
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		id, ok := sstableID(entry.Name())
		if ok && id > maxID {
			maxID = id
		}
	}
	return maxID, nil
}
func (st *StoreManger) nextSSTablePathLocked() string {
	return sstablePath(st.dir, st.nextSSTableID)
}

func sstablePath(dir string, id uint64) string {
	return filepath.Join(dir, fmt.Sprintf("%020d.sst", id))
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
