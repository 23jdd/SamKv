package store

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// RepairReport 描述离线修复过程中发现和处理的文件。
type RepairReport struct {
	ValidTables        int
	WALRecords         int
	Quarantined        []string
	MissingFiles       []string
	IgnoredOrphanFiles []string
}

// RepairDirectory 在持有目录锁时校验 MANIFEST 引用的 SSTable，并隔离损坏文件。
// 修复以 MANIFEST 为权威来源，不会把未发布的孤立 SSTable 自动加入 Store。
func RepairDirectory(dir string) (RepairReport, error) {
	lock, err := acquireDirectoryLock(dir)
	if err != nil {
		return RepairReport{}, err
	}
	defer lock.release()

	var report RepairReport
	recovered := NewMemTable(0)
	if err := RecoverWALFile(filepath.Join(dir, "wal.log"), recovered); err != nil {
		return report, fmt.Errorf("repair WAL: %w", err)
	}
	report.WALRecords = recovered.Len()

	manifest, manifestOK, manifestErr := loadManifest(dir)
	paths, err := filepath.Glob(filepath.Join(dir, "*.sst"))
	if err != nil {
		return report, err
	}
	sort.Strings(paths)

	levels := make(map[string]int)
	candidates := paths
	if manifestErr == nil && manifestOK {
		candidates = make([]string, 0, len(manifest.SSTables))
		known := make(map[string]struct{}, len(manifest.SSTables))
		for _, entry := range manifest.SSTables {
			known[entry.File] = struct{}{}
			levels[entry.File] = entry.Level
			candidates = append(candidates, filepath.Join(dir, entry.File))
		}
		for _, path := range paths {
			if _, ok := known[filepath.Base(path)]; !ok {
				report.IgnoredOrphanFiles = append(report.IgnoredOrphanFiles, path)
			}
		}
	}

	nextManifest := newManifest()
	if manifestErr == nil && manifestOK {
		nextManifest.LastSequence = manifest.LastSequence
	}
	var maxID uint64
	var corrupt []string
	for _, path := range candidates {
		table, err := OpenSStable(path)
		if err == nil {
			_, err = table.Verify()
		}
		if err != nil {
			if table != nil {
				_ = table.Close()
			}
			if errors.Is(err, os.ErrNotExist) {
				report.MissingFiles = append(report.MissingFiles, path)
			} else {
				corrupt = append(corrupt, path)
			}
			continue
		}
		entry := manifestEntryFromSSTable(path, table)
		entry.Level = levels[entry.File]
		nextManifest.SSTables = append(nextManifest.SSTables, entry)
		report.ValidTables++
		if id, ok := sstableID(path); ok && id > maxID {
			maxID = id
		}
		if err := table.Close(); err != nil {
			return report, err
		}
	}
	if maxID >= nextManifest.NextFileID {
		nextManifest.NextFileID = maxID + 1
	}

	if manifestErr == nil && manifestOK {
		source := manifestPath(dir)
		if _, err := os.Stat(source); errors.Is(err, os.ErrNotExist) {
			source = manifestBackupPath(dir)
		}
		backup := manifestPath(dir) + ".repair.bak"
		if err := copyFile(source, backup); err != nil {
			return report, err
		}
	}
	if err := saveManifest(dir, nextManifest); err != nil {
		return report, err
	}
	for _, path := range corrupt {
		destination, err := quarantineSSTable(dir, path)
		if err != nil {
			return report, err
		}
		report.Quarantined = append(report.Quarantined, destination)
	}
	return report, nil
}

func quarantineSSTable(dir, path string) (string, error) {
	corruptDir := filepath.Join(dir, "corrupt")
	if err := os.MkdirAll(corruptDir, 0755); err != nil {
		return "", err
	}
	base := filepath.Base(path)
	destination := filepath.Join(corruptDir, base)
	for suffix := 1; ; suffix++ {
		if _, err := os.Stat(destination); errors.Is(err, os.ErrNotExist) {
			break
		} else if err != nil {
			return "", err
		}
		destination = filepath.Join(corruptDir, fmt.Sprintf("%s.%d", base, suffix))
	}
	if err := os.Rename(path, destination); err != nil {
		return "", err
	}
	return destination, nil
}

func copyFile(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		_ = output.Close()
		if !ok {
			_ = os.Remove(destination)
		}
	}()
	if _, err := io.Copy(output, input); err != nil {
		return err
	}
	if err := output.Sync(); err != nil {
		return err
	}
	if err := output.Close(); err != nil {
		return err
	}
	ok = true
	return nil
}
