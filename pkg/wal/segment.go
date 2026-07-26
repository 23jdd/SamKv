package wal

// 本文件定义 WAL segment 的稳定文件名、目录枚举和编号解析。
// 只有形如 wal-<20 位十进制 ID>.log 的文件会进入恢复顺序，其他文件一律忽略。

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	segmentPrefix  = "wal-"
	segmentSuffix  = ".log"
	segmentIDWidth = 20
)

// Segment 描述一个已经发布到 WAL 目录的 segment。
// ID 决定恢复顺序，Path 是绝对或基于传入 dir 拼出的路径，Size 是枚举时的文件大小快照。
type Segment struct {
	ID   uint64
	Path string
	Size int64
}

// SegmentPath 返回指定 ID 的规范 WAL segment 路径。
// id=0 不是合法持久化编号，内部调用方必须从 1 开始分配。
func SegmentPath(dir string, id uint64) string {
	return filepath.Join(dir, fmt.Sprintf("%s%0*d%s", segmentPrefix, segmentIDWidth, id, segmentSuffix))
}

// ParseSegmentID 从规范文件名中解析 segment ID。
// path 可以是完整路径；编号必须为 20 位十进制且大于 0。
func ParseSegmentID(path string) (uint64, bool) {
	name := filepath.Base(path)
	if !strings.HasPrefix(name, segmentPrefix) || !strings.HasSuffix(name, segmentSuffix) {
		return 0, false
	}
	raw := strings.TrimSuffix(strings.TrimPrefix(name, segmentPrefix), segmentSuffix)
	if len(raw) != segmentIDWidth {
		return 0, false
	}
	id, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || id == 0 {
		return 0, false
	}
	return id, true
}

// ListSegments 返回目录中全部规范 WAL segment，并按 ID 严格递增排序。
// 目录不存在时返回 os.ErrNotExist；临时文件、旧 wal.log 和命名非法文件不会混入结果。
func ListSegments(dir string) ([]Segment, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	segments := make([]Segment, 0)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		id, ok := ParseSegmentID(entry.Name())
		if !ok {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		segments = append(segments, Segment{
			ID:   id,
			Path: filepath.Join(dir, entry.Name()),
			Size: info.Size(),
		})
	}
	sort.Slice(segments, func(i, j int) bool {
		return segments[i].ID < segments[j].ID
	})
	return segments, nil
}
