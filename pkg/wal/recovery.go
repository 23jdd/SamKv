package wal

// 本文件按 segment ID 顺序回放 WAL，并区分可跳过的完整坏帧与无法安全重同步的结构损坏。
// checksum/record 内容错误已经消费完整帧，可以记录后继续；中间 segment 截断或超大长度必须停止。

import (
	"errors"
	"fmt"
	"io"
	"os"
)

var (
	// ErrCorruptSegment 表示 WAL segment 无法在已知 record 边界上继续恢复。
	ErrCorruptSegment = errors.New("wal: corrupt segment")
)

// RecoveryOptions 控制 WAL 回放遇到损坏时的策略。
type RecoveryOptions struct {
	// SkipCorruptedRecords 跳过长度完整但 checksum 或 record 内容无效的单条帧。
	SkipCorruptedRecords bool
	// RepairTrailingPartial 截断最后一个 segment 的半条尾记录；非末段永远不会自动截断。
	RepairTrailingPartial bool
}

// DefaultRecoveryOptions 返回适合 Store 启动恢复的容错策略。
func DefaultRecoveryOptions() RecoveryOptions {
	return RecoveryOptions{
		SkipCorruptedRecords:  true,
		RepairTrailingPartial: true,
	}
}

// RecoveryReport 汇总一次 segment replay 的可观测结果。
type RecoveryReport struct {
	Segments       int
	Records        int
	SkippedRecords int
	TruncatedBytes int64
	LastSegmentID  uint64
}

// ReplaySegments 按 ID 递增回放所有 WAL segment。
// apply 只会收到校验和格式均有效的记录；apply 返回错误时立即停止且不修改磁盘。
func ReplaySegments(
	dir string,
	options RecoveryOptions,
	apply func(*Record) error,
) (RecoveryReport, error) {
	segments, err := ListSegments(dir)
	if err != nil {
		return RecoveryReport{}, err
	}
	report := RecoveryReport{Segments: len(segments)}
	if len(segments) > 0 {
		report.LastSegmentID = segments[len(segments)-1].ID
	}

	for index, segment := range segments {
		last := index == len(segments)-1
		if err := replaySegment(segment, last, options, apply, &report); err != nil {
			return report, err
		}
	}
	return report, nil
}

func replaySegment(
	segment Segment,
	last bool,
	options RecoveryOptions,
	apply func(*Record) error,
	report *RecoveryReport,
) error {
	flags := os.O_RDONLY
	if last && options.RepairTrailingPartial {
		flags = os.O_RDWR
	}
	file, err := os.OpenFile(segment.Path, flags, 0644)
	if err != nil {
		return err
	}
	defer file.Close()

	var lastGoodOffset int64
	for {
		recordOffset, err := file.Seek(0, io.SeekCurrent)
		if err != nil {
			return err
		}
		record, readErr := ReadRecord(file)
		if errors.Is(readErr, io.EOF) {
			return nil
		}
		if errors.Is(readErr, io.ErrUnexpectedEOF) {
			if !last || !options.RepairTrailingPartial {
				return fmt.Errorf("%w: segment %d offset %d: %w", ErrCorruptSegment, segment.ID, recordOffset, readErr)
			}
			info, statErr := file.Stat()
			if statErr != nil {
				return statErr
			}
			report.TruncatedBytes += info.Size() - lastGoodOffset
			if err := file.Truncate(lastGoodOffset); err != nil {
				return err
			}
			return file.Sync()
		}
		if readErr != nil {
			if options.SkipCorruptedRecords &&
				(errors.Is(readErr, ErrChecksum) || errors.Is(readErr, ErrInvalidRecord)) {
				currentOffset, seekErr := file.Seek(0, io.SeekCurrent)
				if seekErr != nil {
					return seekErr
				}
				lastGoodOffset = currentOffset
				report.SkippedRecords++
				continue
			}
			return fmt.Errorf("%w: segment %d offset %d: %w", ErrCorruptSegment, segment.ID, recordOffset, readErr)
		}

		currentOffset, err := file.Seek(0, io.SeekCurrent)
		if err != nil {
			return err
		}
		lastGoodOffset = currentOffset
		if apply != nil {
			if err := apply(record); err != nil {
				return err
			}
		}
		report.Records++
	}
}
