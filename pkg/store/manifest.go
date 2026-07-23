package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

const manifestFileName = "MANIFEST"

// Manifest 记录当前 Store 认可的 SSTable 列表和下一个文件编号。
// WAL 负责恢复尚未 checkpoint 的数据，Manifest 负责恢复已经落盘成 SSTable 的数据。
type Manifest struct {
	NextFileID uint64            `json:"next_file_id"`
	SSTables   []ManifestSSTable `json:"sstables"`
}

// ManifestSSTable 是 Manifest 中的一条 SSTable 元数据。
// Level 预留给后续 compaction 分层使用；当前实现先固定为 0。
type ManifestSSTable struct {
	File        string `json:"file"`
	Level       int    `json:"level"`
	MinKey      string `json:"min_key"`
	MaxKey      string `json:"max_key"`
	RecordCount uint64 `json:"record_count"`
}

func newManifest() Manifest {
	return Manifest{NextFileID: 1}
}

// manifestPath 返回 Manifest 文件在当前 Store 目录中的完整路径。
func manifestPath(dir string) string {
	return filepath.Join(dir, manifestFileName)
}

// loadManifest 从磁盘读取 Manifest。
// 第二个返回值表示文件是否存在；不存在时返回一个空的新 Manifest，方便兼容旧数据目录。
func loadManifest(dir string) (Manifest, bool, error) {
	data, err := os.ReadFile(manifestPath(dir))
	if errors.Is(err, os.ErrNotExist) {
		return newManifest(), false, nil
	}
	if err != nil {
		return Manifest{}, false, err
	}

	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return Manifest{}, true, err
	}
	if manifest.NextFileID == 0 {
		manifest.NextFileID = 1
	}
	return manifest, true, nil
}

// saveManifest 以临时文件 + rename 的方式写入 Manifest。
// 这样可以避免进程在写入中途崩溃时留下半截 MANIFEST。
func saveManifest(dir string, manifest Manifest) error {
	if manifest.NextFileID == 0 {
		manifest.NextFileID = 1
	}

	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	tmpPath := manifestPath(dir) + ".tmp"
	file, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}

	ok := false
	defer func() {
		if !ok {
			_ = file.Close()
			_ = os.Remove(tmpPath)
		}
	}()

	if err := writeAll(file, data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	ok = true
	return os.Rename(tmpPath, manifestPath(dir))
}

// manifestEntryFromSSTable 从 SSTable 的 MetaBlock 中提取 Manifest 需要保存的元数据。
func manifestEntryFromSSTable(path string, table *SStable) ManifestSSTable {
	meta := table.Meta()
	return ManifestSSTable{
		File:        filepath.Base(path),
		Level:       0,
		MinKey:      meta.MinKey,
		MaxKey:      meta.MaxKey,
		RecordCount: meta.RecordCount,
	}
}
