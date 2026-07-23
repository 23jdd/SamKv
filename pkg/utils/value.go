package utils

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	valueVersion byte = 1

	// CompressionNone 表示 Message 保存原始日志内容。
	CompressionNone byte = 0

	// CompressionGzip 表示 Message 使用 gzip 压缩。
	// 这里使用标准库 gzip，避免额外引入 Snappy/Zstd 依赖。
	CompressionGzip byte = 1
)

var (
	// ErrInvalidValue 表示 value 的二进制格式不合法。
	ErrInvalidValue = errors.New("utils: invalid value")

	// ErrUnsupportedCompression 表示遇到了当前版本不支持的压缩算法。
	ErrUnsupportedCompression = errors.New("utils: unsupported compression")
)

// Value 保存日志内容。
// Message 字段存储的是压缩后的内容；使用 DecompressedMessage 可以还原原始日志。
type Value struct {
	Timestamp   int64
	Labels      []Label
	Message     []byte
	Compression byte
}

// NewValue 创建一个 gzip 压缩的日志 Value。
func NewValue(timestamp int64, labels []Label, message []byte) (Value, error) {
	return NewValueWithCompression(timestamp, labels, message, CompressionGzip)
}

// NewValueWithCompression 创建指定压缩格式的日志 Value。
func NewValueWithCompression(timestamp int64, labels []Label, message []byte, compression byte) (Value, error) {
	labels = NormalizeLabels(labels)
	compressed, err := compressMessage(message, compression)
	if err != nil {
		return Value{}, err
	}
	return Value{Timestamp: timestamp, Labels: labels, Message: compressed, Compression: compression}, nil
}

// DecompressedMessage 返回解压后的原始日志内容。
func (v Value) DecompressedMessage() ([]byte, error) {
	return decompressMessage(v.Message, v.Compression)
}

// MarshalBinary 将 Value 编码成二进制格式。
// 格式：version、compression、timestamp、labels、compressed message。
func (v Value) MarshalBinary() ([]byte, error) {
	labels := NormalizeLabels(v.Labels)
	var buf bytes.Buffer
	buf.WriteByte(valueVersion)
	buf.WriteByte(v.Compression)
	if err := writeInt64(&buf, v.Timestamp); err != nil {
		return nil, err
	}
	if err := writeUint32(&buf, uint32(len(labels))); err != nil {
		return nil, err
	}
	for _, label := range labels {
		if err := validateLabel(label); err != nil {
			return nil, err
		}
		if err := writeBytes(&buf, []byte(label.Name)); err != nil {
			return nil, err
		}
		if err := writeBytes(&buf, []byte(label.Value)); err != nil {
			return nil, err
		}
	}
	if err := writeBytes(&buf, v.Message); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// UnmarshalValue 解码 MarshalBinary 生成的二进制 value。
func UnmarshalValue(data []byte) (Value, error) {
	reader := bytes.NewReader(data)
	version, err := reader.ReadByte()
	if err != nil {
		return Value{}, ErrInvalidValue
	}
	if version != valueVersion {
		return Value{}, fmt.Errorf("%w: version %d", ErrInvalidValue, version)
	}
	compression, err := reader.ReadByte()
	if err != nil {
		return Value{}, ErrInvalidValue
	}
	if compression != CompressionNone && compression != CompressionGzip {
		return Value{}, ErrUnsupportedCompression
	}
	timestamp, err := readInt64(reader)
	if err != nil {
		return Value{}, err
	}
	labelCount, err := readUint32(reader)
	if err != nil {
		return Value{}, err
	}
	labels := make([]Label, 0, labelCount)
	for i := uint32(0); i < labelCount; i++ {
		name, err := readBytes(reader)
		if err != nil {
			return Value{}, err
		}
		value, err := readBytes(reader)
		if err != nil {
			return Value{}, err
		}
		labels = append(labels, Label{Name: string(name), Value: string(value)})
	}
	message, err := readBytes(reader)
	if err != nil {
		return Value{}, err
	}
	if reader.Len() != 0 {
		return Value{}, ErrInvalidValue
	}
	return Value{Timestamp: timestamp, Labels: labels, Message: message, Compression: compression}, nil
}

func compressMessage(message []byte, compression byte) ([]byte, error) {
	switch compression {
	case CompressionNone:
		out := make([]byte, len(message))
		copy(out, message)
		return out, nil
	case CompressionGzip:
		var buf bytes.Buffer
		writer := gzip.NewWriter(&buf)
		if _, err := writer.Write(message); err != nil {
			_ = writer.Close()
			return nil, err
		}
		if err := writer.Close(); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	default:
		return nil, ErrUnsupportedCompression
	}
}

func decompressMessage(message []byte, compression byte) ([]byte, error) {
	switch compression {
	case CompressionNone:
		out := make([]byte, len(message))
		copy(out, message)
		return out, nil
	case CompressionGzip:
		reader, err := gzip.NewReader(bytes.NewReader(message))
		if err != nil {
			return nil, err
		}
		defer reader.Close()
		return io.ReadAll(reader)
	default:
		return nil, ErrUnsupportedCompression
	}
}

func writeInt64(w io.Writer, v int64) error {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(v))
	_, err := w.Write(buf[:])
	return err
}

func readInt64(r io.Reader) (int64, error) {
	var buf [8]byte
	if _, err := io.ReadFull(r, buf[:]); err != nil {
		return 0, ErrInvalidValue
	}
	return int64(binary.BigEndian.Uint64(buf[:])), nil
}

func writeUint32(w io.Writer, v uint32) error {
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], v)
	_, err := w.Write(buf[:])
	return err
}

func readUint32(r io.Reader) (uint32, error) {
	var buf [4]byte
	if _, err := io.ReadFull(r, buf[:]); err != nil {
		return 0, ErrInvalidValue
	}
	return binary.BigEndian.Uint32(buf[:]), nil
}

func writeBytes(w io.Writer, data []byte) error {
	if err := writeUint32(w, uint32(len(data))); err != nil {
		return err
	}
	_, err := w.Write(data)
	return err
}

func readBytes(r io.Reader) ([]byte, error) {
	length, err := readUint32(r)
	if err != nil {
		return nil, err
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, ErrInvalidValue
	}
	return data, nil
}
