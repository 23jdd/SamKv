package utils

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	keyTimestampSize = 8
	keySequenceSize  = 8
	keySeparator     = byte(0)
)

var (
	// ErrInvalidKey 表示 key 的二进制格式不合法。
	ErrInvalidKey = errors.New("utils: invalid key")

	// ErrInvalidLabel 表示标签名或标签值包含不允许编码的字符。
	ErrInvalidLabel = errors.New("utils: invalid label")
)

// Label 表示一组日志标签。
// 标签会按 Name、Value 字典序排序后编码，保证相同标签集合得到相同 key。
type Label struct {
	Name  string
	Value string
}

// Key 表示解码后的复合 key。
type Key struct {
	Timestamp int64
	Labels    []Label
	Sequence  uint64
}

// EncodeKey 将时间戳、标签和序列号编码成可按字节序比较的 key。
// 布局：
//
//	[8 bytes timestamp][sorted label bytes][0x00][8 bytes sequence]
//
// timestamp 使用翻转符号位的 big-endian int64，保证 int64 的自然顺序等于字节序。
func EncodeKey(timestamp int64, labels []Label, sequence uint64) ([]byte, error) {
	labels = NormalizeLabels(labels)
	labelBytes, err := encodeLabels(labels)
	if err != nil {
		return nil, err
	}

	key := make([]byte, 0, keyTimestampSize+len(labelBytes)+1+keySequenceSize)
	var ts [keyTimestampSize]byte
	binary.BigEndian.PutUint64(ts[:], encodeOrderedInt64(timestamp))
	key = append(key, ts[:]...)
	key = append(key, labelBytes...)
	key = append(key, keySeparator)

	var seq [keySequenceSize]byte
	binary.BigEndian.PutUint64(seq[:], sequence)
	key = append(key, seq[:]...)
	return key, nil
}

// DecodeKey 将 EncodeKey 生成的二进制 key 还原成结构化字段。
func DecodeKey(data []byte) (Key, error) {
	if len(data) < keyTimestampSize+1+keySequenceSize {
		return Key{}, ErrInvalidKey
	}

	timestamp := decodeOrderedInt64(binary.BigEndian.Uint64(data[:keyTimestampSize]))
	labelsEnd := len(data) - keySequenceSize - 1
	if labelsEnd < keyTimestampSize || data[labelsEnd] != keySeparator {
		return Key{}, ErrInvalidKey
	}

	labels, err := decodeLabels(data[keyTimestampSize:labelsEnd])
	if err != nil {
		return Key{}, err
	}
	sequence := binary.BigEndian.Uint64(data[len(data)-keySequenceSize:])
	return Key{Timestamp: timestamp, Labels: labels, Sequence: sequence}, nil
}

// EncodeKeyString 生成便于调试和日志展示的文本 key。
// 文本 key 不用于磁盘排序；真正存储和范围查询应使用 EncodeKey。
func EncodeKeyString(timestamp time.Time, labels []Label, sequence uint64) (string, error) {
	labels = NormalizeLabels(labels)
	encoded, err := encodeLabels(labels)
	if err != nil {
		return "", err
	}
	if len(encoded) == 0 {
		return fmt.Sprintf("%s|%05d", timestamp.UTC().Format(time.RFC3339Nano), sequence), nil
	}
	return fmt.Sprintf("%s|%s|%05d", timestamp.UTC().Format(time.RFC3339Nano), encoded, sequence), nil
}

// NormalizeLabels 返回按 Name、Value 排序后的标签副本。
func NormalizeLabels(labels []Label) []Label {
	out := make([]Label, len(labels))
	copy(out, labels)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Name == out[j].Name {
			return out[i].Value < out[j].Value
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// TimePrefix 返回只包含时间戳的 key 前缀，可用于按时间做范围扫描边界。
func TimePrefix(timestamp int64) []byte {
	prefix := make([]byte, keyTimestampSize)
	binary.BigEndian.PutUint64(prefix, encodeOrderedInt64(timestamp))
	return prefix
}

func encodeOrderedInt64(v int64) uint64 {
	return uint64(v) ^ (uint64(1) << 63)
}

func decodeOrderedInt64(v uint64) int64 {
	return int64(v ^ (uint64(1) << 63))
}

func encodeLabels(labels []Label) ([]byte, error) {
	var buf bytes.Buffer
	for i, label := range labels {
		if err := validateLabel(label); err != nil {
			return nil, err
		}
		if i > 0 {
			buf.WriteByte('|')
		}
		buf.WriteString(escapeLabelPart(label.Name))
		buf.WriteByte('=')
		buf.WriteString(escapeLabelPart(label.Value))
	}
	return buf.Bytes(), nil
}

func decodeLabels(data []byte) ([]Label, error) {
	if len(data) == 0 {
		return nil, nil
	}
	parts := strings.Split(string(data), "|")
	labels := make([]Label, 0, len(parts))
	for _, part := range parts {
		name, value, ok := strings.Cut(part, "=")
		if !ok {
			return nil, ErrInvalidKey
		}
		decodedName, err := unescapeLabelPart(name)
		if err != nil {
			return nil, err
		}
		decodedValue, err := unescapeLabelPart(value)
		if err != nil {
			return nil, err
		}
		labels = append(labels, Label{Name: decodedName, Value: decodedValue})
	}
	return labels, nil
}

func validateLabel(label Label) error {
	if label.Name == "" {
		return fmt.Errorf("%w: empty label name", ErrInvalidLabel)
	}
	if strings.ContainsRune(label.Name, rune(keySeparator)) || strings.ContainsRune(label.Value, rune(keySeparator)) {
		return fmt.Errorf("%w: label contains NUL", ErrInvalidLabel)
	}
	return nil
}

func escapeLabelPart(s string) string {
	s = strings.ReplaceAll(s, `%`, `%25`)
	s = strings.ReplaceAll(s, `|`, `%7C`)
	s = strings.ReplaceAll(s, `=`, `%3D`)
	return s
}

func unescapeLabelPart(s string) (string, error) {
	var buf strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '%' {
			buf.WriteByte(s[i])
			continue
		}
		if i+2 >= len(s) {
			return "", ErrInvalidKey
		}
		switch s[i+1 : i+3] {
		case "25":
			buf.WriteByte('%')
		case "7C":
			buf.WriteByte('|')
		case "3D":
			buf.WriteByte('=')
		default:
			return "", ErrInvalidKey
		}
		i += 2
	}
	return buf.String(), nil
}
