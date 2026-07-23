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

	"github.com/23jdd/SamKv/pkg/wal"
)

// StoreManger 管理 MemTable、WAL 和当前已知的 SSTable 列表。
// 写入先进入 WAL 和 MemTable，Checkpoint 后数据会转移到 SSTable。
type StoreManger struct {
	mu sync.RWMutex

	dir string
	mem *MemTable
	wm  *wal.WalManger

	sstables      []*SStable
	nextSSTableID uint64
	manifest      Manifest
}

// NewStoreManger 创建 Store，加载 Manifest/SSTable，并自动回放尚未 checkpoint 的 WAL。
func NewStoreManger(dir string, limit int) (*StoreManger, error) {
	wm, err := wal.New(dir)
	if err != nil {
		return nil, err
	}

	st := &StoreManger{
		dir:           dir,
		mem:           NewMemTable(limit),
		wm:            wm,
		nextSSTableID: 1,
		manifest:      newManifest(),
	}
	if err := st.loadSSTables(); err != nil {
		st.closeSSTables()
		_ = wm.Close()
		return nil, err
	}
	if err := RecoverWALFile(filepath.Join(dir, "wal.log"), st.mem); err != nil {
		st.closeSSTables()
		_ = wm.Close()
		return nil, err
	}
	return st, nil
}

// Put 写入 key/value。
// 顺序是先写 WAL，再写 MemTable。
func (st *StoreManger) Put(key string, val string) error {
	st.mu.Lock()
	defer st.mu.Unlock()

	if err := st.wm.AppendRecord(wal.PutRecord([]byte(key), []byte(val))); err != nil {
		return err
	}
	return st.mem.Put(key, val)
}

// Get 查询 key。
// 查询顺序是 MemTable -> 新 SSTable -> 旧 SSTable；遇到墓碑会直接返回不存在。
func (st *StoreManger) Get(key string) (string, bool) {
	st.mu.RLock()
	defer st.mu.RUnlock()

	if entry, ok := st.mem.GetEntry(key); ok {
		if entry.Deleted {
			return "", false
		}
		return entry.Value, true
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

// Delete 写入 key 的墓碑。
// 墓碑会覆盖旧 SSTable 中可能存在的旧值。
func (st *StoreManger) Delete(key string) error {
	st.mu.Lock()
	defer st.mu.Unlock()

	if err := st.wm.AppendRecord(wal.DeleteRecord([]byte(key))); err != nil {
		return err
	}
	return st.mem.Delete(key)
}

// Close 关闭 Store 持有的 WAL 和 SSTable 文件句柄。
func (st *StoreManger) Close() error {
	st.mu.Lock()
	defer st.mu.Unlock()

	tableErr := st.closeSSTables()
	return errors.Join(tableErr, st.wm.Close())
}

func (st *StoreManger) closeSSTables() error {
	var closeErr error
	for _, table := range st.sstables {
		closeErr = errors.Join(closeErr, table.Close())
	}
	return closeErr
}

// Checkpoint 将当前 MemTable 写成 SSTable，然后清空 WAL。
// 顺序必须是：Flush WAL -> Write SSTable -> Publish Manifest -> Reset WAL -> Clear MemTable。
func (st *StoreManger) Checkpoint() (string, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.checkpointLocked()
}

func (st *StoreManger) checkpointLocked() (string, error) {
	if err := st.wm.Flush(); err != nil {
		return "", err
	}

	records := st.mem.Flush()
	var path string
	if len(records) > 0 {
		path = st.nextSSTablePathLocked()
		table, err := WriteSStable(path, records)
		if err != nil {
			return "", err
		}

		nextManifest := st.manifest
		nextManifest.SSTables = append([]ManifestSSTable(nil), st.manifest.SSTables...)
		nextManifest.SSTables = append(nextManifest.SSTables, manifestEntryFromSSTable(path, table))
		nextManifest.NextFileID = st.nextSSTableID + 1
		if err := saveManifest(st.dir, nextManifest); err != nil {
			_ = table.Close()
			_ = os.Remove(path)
			return "", err
		}

		st.sstables = append(st.sstables, table)
		st.nextSSTableID++
		st.manifest = nextManifest
	}

	if err := st.wm.Reset(); err != nil {
		return path, err
	}
	st.mem.Clear()
	return path, nil
}

// ReLoad 清空当前 MemTable，并重新回放 wal.log。
// 正常启动不需要手动调用；NewStoreManger 已经自动完成恢复。
func (st *StoreManger) ReLoad() error {
	st.mu.Lock()
	defer st.mu.Unlock()

	st.mem.Clear()
	return RecoverWALFile(filepath.Join(st.dir, "wal.log"), st.mem)
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
			st.closeSSTables()
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

// loadSSTablesFromManifest 按 Manifest 中记录的顺序加载 SSTable。
// 这个顺序会影响查询覆盖关系：后写入的 SSTable 会在 Get 时优先命中。
func (st *StoreManger) loadSSTablesFromManifest(manifest Manifest) error {
	st.manifest = manifest
	st.nextSSTableID = manifest.NextFileID

	var maxID uint64
	for _, entry := range manifest.SSTables {
		path := filepath.Join(st.dir, entry.File)
		table, err := OpenSStable(path)
		if err != nil {
			st.closeSSTables()
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

// manifestFromSSTables 用于兼容旧目录：没有 MANIFEST 时，先扫描已有 *.sst 再生成 Manifest。
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
