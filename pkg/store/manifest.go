package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const manifestFileName = "MANIFEST"

// Manifest 记录当前 Store 认可的 SSTable 列表和下一个文件编号。
// WAL 负责恢复尚未 checkpoint 的数据，Manifest 负责恢复已经落盘成 SSTable 的数据。
type Manifest struct {
	NextFileID   uint64            `json:"next_file_id"`
	LastSequence uint64            `json:"last_sequence"`
	SSTables     []ManifestSSTable `json:"sstables"`
}

// ManifestSSTable 是 Manifest 中的一条 SSTable 元数据。
// Level 预留给后续 compaction 分层使用；新写入文件从 L0 开始。
type ManifestSSTable struct {
	File             string            `json:"file"`
	Level            int               `json:"level"`
	MinKey           string            `json:"min_key"`
	MaxKey           string            `json:"max_key"`
	RecordCount      uint64            `json:"record_count"`
	HasTimeRange     bool              `json:"has_time_range,omitempty"`
	MinTimestamp     int64             `json:"min_timestamp,omitempty"`
	MaxTimestamp     int64             `json:"max_timestamp,omitempty"`
	LabelCardinality map[string]uint64 `json:"label_cardinality,omitempty"`
}

func newManifest() Manifest {
	return Manifest{NextFileID: 1}
}

func manifestPath(dir string) string {
	return filepath.Join(dir, manifestFileName)
}

func manifestBackupPath(dir string) string {
	return manifestPath(dir) + ".bak"
}

// loadManifest 从磁盘读取 Manifest。
// 主文件不存在时会尝试备份文件，用于恢复 Windows 上替换文件中途发生的崩溃。
func loadManifest(dir string) (Manifest, bool, error) {
	manifest, err := readManifest(manifestPath(dir))
	if err == nil {
		return manifest, true, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return Manifest{}, true, err
	}

	manifest, err = readManifest(manifestBackupPath(dir))
	if err == nil {
		return manifest, true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return newManifest(), false, nil
	}
	return Manifest{}, true, err
}

func readManifest(path string) (Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, err
	}

	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("store: decode manifest: %w", err)
	}
	if manifest.NextFileID == 0 {
		manifest.NextFileID = 1
	}
	if err := validateManifest(manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func validateManifest(manifest Manifest) error {
	seen := make(map[string]struct{}, len(manifest.SSTables))
	for _, entry := range manifest.SSTables {
		if entry.File == "" || filepath.Base(entry.File) != entry.File || !strings.HasSuffix(entry.File, ".sst") {
			return fmt.Errorf("store: invalid manifest sstable path %q", entry.File)
		}
		if entry.Level < 0 {
			return fmt.Errorf("store: invalid manifest level %d", entry.Level)
		}
		if _, ok := seen[entry.File]; ok {
			return fmt.Errorf("store: duplicate manifest sstable %q", entry.File)
		}
		seen[entry.File] = struct{}{}
	}
	return nil
}

// saveManifest 先完整写入临时文件，再发布为 MANIFEST。
// Windows 不能直接用 rename 覆盖已有文件，因此使用 .bak 保留旧版本并支持崩溃恢复。
func saveManifest(dir string, manifest Manifest) error {
	if manifest.NextFileID == 0 {
		manifest.NextFileID = 1
	}
	if err := validateManifest(manifest); err != nil {
		return err
	}

	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, 10)

	targetPath := manifestPath(dir)
	tmpPath := targetPath + ".tmp"
	backupPath := manifestBackupPath(dir)
	file, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}

	published := false
	defer func() {
		_ = file.Close()
		if !published {
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

	// Unix 可以直接覆盖目标文件，先尝试这条最短路径。
	if err := os.Rename(tmpPath, targetPath); err == nil {
		published = true
		_ = os.Remove(backupPath)
		return nil
	} else if _, statErr := os.Stat(targetPath); statErr != nil {
		return err
	}

	// Windows 路径：旧 MANIFEST 先改名为备份，再发布新文件。
	_ = os.Remove(backupPath)
	if err := os.Rename(targetPath, backupPath); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, targetPath); err != nil {
		_ = os.Rename(backupPath, targetPath)
		return err
	}
	published = true
	_ = os.Remove(backupPath)
	return nil
}

// manifestEntryFromSSTable 从 SSTable 的 MetaBlock 中提取 Manifest 需要保存的元数据。
func manifestEntryFromSSTable(path string, table *SStable) ManifestSSTable {
	meta := table.Meta()
	return ManifestSSTable{
		File:             filepath.Base(path),
		Level:            0,
		MinKey:           meta.MinKey,
		MaxKey:           meta.MaxKey,
		RecordCount:      meta.RecordCount,
		HasTimeRange:     meta.HasTimeRange,
		MinTimestamp:     meta.MinTimestamp,
		MaxTimestamp:     meta.MaxTimestamp,
		LabelCardinality: meta.LabelCardinality,
	}
}
