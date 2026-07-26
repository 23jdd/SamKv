package store

// 本文件定义世代式 Manifest 文件名和目录枚举。
// 只有 MANIFEST-<20 位十进制 ID> 会被 CURRENT 引用，.tmp、.bak 和近似命名不会参与选择。

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	currentFileName          = "CURRENT"
	manifestGenerationPrefix = "MANIFEST-"
	manifestGenerationWidth  = 20
)

type manifestGeneration struct {
	ID   uint64
	Name string
	Path string
}

func manifestGenerationName(id uint64) string {
	return fmt.Sprintf("%s%0*d", manifestGenerationPrefix, manifestGenerationWidth, id)
}

func manifestGenerationPath(dir string, id uint64) string {
	return filepath.Join(dir, manifestGenerationName(id))
}

func parseManifestGeneration(path string) (uint64, bool) {
	name := filepath.Base(path)
	if !strings.HasPrefix(name, manifestGenerationPrefix) {
		return 0, false
	}
	raw := strings.TrimPrefix(name, manifestGenerationPrefix)
	if len(raw) != manifestGenerationWidth {
		return 0, false
	}
	id, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || id == 0 {
		return 0, false
	}
	return id, true
}

func listManifestGenerations(dir string) ([]manifestGeneration, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	generations := make([]manifestGeneration, 0)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		id, ok := parseManifestGeneration(entry.Name())
		if !ok {
			continue
		}
		generations = append(generations, manifestGeneration{
			ID:   id,
			Name: entry.Name(),
			Path: filepath.Join(dir, entry.Name()),
		})
	}
	sort.Slice(generations, func(i, j int) bool {
		return generations[i].ID < generations[j].ID
	})
	return generations, nil
}

func nextManifestGeneration(dir string) (uint64, error) {
	generations, err := listManifestGenerations(dir)
	if err != nil {
		return 0, err
	}
	if len(generations) == 0 {
		return 1, nil
	}
	return generations[len(generations)-1].ID + 1, nil
}

func currentPath(dir string) string {
	return filepath.Join(dir, currentFileName)
}

func currentBackupPath(dir string) string {
	return currentPath(dir) + ".bak"
}

// activeManifestPath 解析 CURRENT；没有 CURRENT 时兼容固定名 MANIFEST/MANIFEST.bak。
func activeManifestPath(dir string) (string, bool, error) {
	name, exists, err := readCurrentManifestName(dir)
	if err != nil {
		return "", true, err
	}
	if exists {
		return filepath.Join(dir, name), true, nil
	}
	for _, path := range []string{manifestPath(dir), manifestBackupPath(dir)} {
		if _, err := os.Stat(path); err == nil {
			return path, true, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", true, err
		}
	}
	return "", false, nil
}

func readCurrentManifestName(dir string) (string, bool, error) {
	for _, path := range []string{currentPath(dir), currentBackupPath(dir)} {
		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", true, err
		}
		name := strings.TrimSpace(string(data))
		if _, ok := parseManifestGeneration(name); !ok || filepath.Base(name) != name {
			return "", true, fmt.Errorf("store: invalid CURRENT target %q", name)
		}
		return name, true, nil
	}
	return "", false, nil
}

func publishManifestGeneration(dir string, data []byte) error {
	generation, err := nextManifestGeneration(dir)
	if err != nil {
		return err
	}
	name := manifestGenerationName(generation)
	target := filepath.Join(dir, name)
	tmp, err := os.CreateTemp(dir, "."+name+".tmp-")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	published := false
	defer func() {
		_ = tmp.Close()
		if !published {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := writeAll(tmp, data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, target); err != nil {
		return err
	}
	published = true
	if err := syncStoreDirectory(dir); err != nil {
		return err
	}
	if err := publishCurrent(dir, name); err != nil {
		return err
	}

	// CURRENT 已经提交；旧世代清理失败只会占用空间，不能把已提交状态降级成调用失败。
	pruneManifestGenerations(dir, generation, 2)
	return nil
}

func publishCurrent(dir, manifestName string) error {
	tmp, err := os.CreateTemp(dir, "."+currentFileName+".tmp-")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	published := false
	defer func() {
		_ = tmp.Close()
		if !published {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := writeAll(tmp, []byte(manifestName+"\n")); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	target := currentPath(dir)
	if err := os.Rename(tmpPath, target); err != nil {
		backup := currentBackupPath(dir)
		_ = os.Remove(backup)
		if renameErr := os.Rename(target, backup); renameErr != nil && !errors.Is(renameErr, os.ErrNotExist) {
			return errors.Join(err, renameErr)
		}
		if renameErr := os.Rename(tmpPath, target); renameErr != nil {
			_ = os.Rename(backup, target)
			return renameErr
		}
		_ = os.Remove(backup)
	}
	published = true
	// CURRENT 已经可见，目录同步失败时不能让调用方删除它引用的 SSTable。
	_ = syncStoreDirectory(dir)
	return nil
}

func pruneManifestGenerations(dir string, current uint64, keep int) {
	generations, err := listManifestGenerations(dir)
	if err != nil || len(generations) <= keep {
		return
	}
	cutoff := current
	if keep > 1 && current >= uint64(keep-1) {
		cutoff = current - uint64(keep-1)
	}
	for _, generation := range generations {
		if generation.ID >= cutoff {
			continue
		}
		_ = os.Remove(generation.Path)
	}
	_ = syncStoreDirectory(dir)
}
