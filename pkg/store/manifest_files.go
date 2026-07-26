package store

// 本文件定义世代式 Manifest 文件名和目录枚举。
// 只有 MANIFEST-<20 位十进制 ID> 会被 CURRENT 引用，.tmp、.bak 和近似命名不会参与选择。

import (
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
