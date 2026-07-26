package utils

// 本文件定义日志 Value 的压缩、二进制序列化和严格解码。
// Value.Message 是编码后的载荷；业务读取必须通过 DecompressedMessage 获取原文。

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/golang/snappy"
	"github.com/klauspost/compress/zstd"
	"github.com/pierrec/lz4/v4"
)

const (
	valueVersion byte = 1
	// MaxValueMessageSize 限制单条日志解压后的消息大小，防止压缩炸弹耗尽内存。
	MaxValueMessageSize = 64 * 1024 * 1024
)

// CompressionType 是 Value 载荷压缩算法的稳定磁盘编号。
// 已发布编号只能追加，不能复用或重排，否则旧 Value 会按错误算法解码。
type CompressionType byte

const (
	// CompressionNone 表示 Message 保存原始日志内容。
	CompressionNone CompressionType = iota
	// CompressionGzip 表示 Message 使用标准库 gzip 压缩。
	CompressionGzip
	// CompressionSnappy 表示 Message 使用 Snappy 压缩，适合低延迟写入。
	CompressionSnappy
	// CompressionLZ4 表示 Message 使用 LZ4 frame 压缩，适合高吞吐读取。
	CompressionLZ4
	// CompressionZstd 表示 Message 使用 Zstandard 压缩，适合更关注压缩率的场景。
	CompressionZstd
)

// Valid 报告压缩编号是否属于当前版本已定义的磁盘格式。
func (c CompressionType) Valid() bool {
	return c >= CompressionNone && c <= CompressionZstd
}

// String 返回配置文件和 HTTP 参数使用的小写算法名。
func (c CompressionType) String() string {
	switch c {
	case CompressionNone:
		return "none"
	case CompressionGzip:
		return "gzip"
	case CompressionSnappy:
		return "snappy"
	case CompressionLZ4:
		return "lz4"
	case CompressionZstd:
		return "zstd"
	default:
		return "unknown"
	}
}

// ParseCompressionType 把配置名称转换为稳定编号；名称忽略首尾空白和大小写。
func ParseCompressionType(value string) (CompressionType, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "none":
		return CompressionNone, nil
	case "gzip":
		return CompressionGzip, nil
	case "snappy":
		return CompressionSnappy, nil
	case "lz4":
		return CompressionLZ4, nil
	case "zstd":
		return CompressionZstd, nil
	default:
		return 0, fmt.Errorf("%w: %q", ErrUnsupportedCompression, value)
	}
}

var (
	zstdOnce    sync.Once
	zstdEncoder *zstd.Encoder
	zstdDecoder *zstd.Decoder
	zstdInitErr error
)
var (
	// ErrInvalidValue 表示 value 的二进制格式不合法。
	ErrInvalidValue = errors.New("utils: invalid value")

	// ErrUnsupportedCompression 表示遇到了当前版本不支持的压缩算法。
	ErrUnsupportedCompression = errors.New("utils: unsupported compression")

	// ErrValueTooLarge 表示原始消息或解压输出超过 MaxValueMessageSize。
	ErrValueTooLarge = errors.New("utils: value message too large")
)

// Value 保存日志内容。
// 标签已经编码在 Key 中，所以 Value 只保存时间戳和压缩后的日志内容。
type Value struct {
	Timestamp   int64
	Message     []byte
	Compression CompressionType
}

// NewValue 创建一个 gzip 压缩的日志 Value。
// message 可以为空；返回值拥有独立压缩缓冲，调用方之后可以安全复用原切片。
func NewValue(timestamp int64, message []byte) (Value, error) {
	return NewValueWithCompression(timestamp, message, CompressionGzip)
}

// NewValueWithCompression 创建指定压缩格式的日志 Value。
// compression 必须是已定义算法；message 超过 MaxValueMessageSize 时返回 ErrValueTooLarge。
func NewValueWithCompression(timestamp int64, message []byte, compression CompressionType) (Value, error) {
	compressed, err := compressMessage(message, compression)
	if err != nil {
		return Value{}, err
	}
	return Value{Timestamp: timestamp, Message: compressed, Compression: compression}, nil
}

// DecompressedMessage 返回解压后的原始日志内容。
// 每次调用都返回新切片；损坏数据返回对应解码错误，解压输出过大返回 ErrValueTooLarge。
func (v Value) DecompressedMessage() ([]byte, error) {
	return decompressMessage(v.Message, v.Compression)
}

// MarshalBinary 将 Value 编码成二进制格式。
// 格式：version、compression、timestamp、compressed message。
func (v Value) MarshalBinary() ([]byte, error) {
	if !v.Compression.Valid() {
		return nil, ErrUnsupportedCompression
	}
	var buf bytes.Buffer
	buf.WriteByte(valueVersion)
	buf.WriteByte(byte(v.Compression))
	if err := writeInt64(&buf, v.Timestamp); err != nil {
		return nil, err
	}
	if err := writeBytes(&buf, v.Message); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// UnmarshalValue 解码 MarshalBinary 生成的二进制 value。
// 输入必须恰好包含一条 Value；尾随字节、截断数据和未知版本都会被拒绝。
func UnmarshalValue(data []byte) (Value, error) {
	reader := bytes.NewReader(data)
	version, err := reader.ReadByte()
	if err != nil {
		return Value{}, ErrInvalidValue
	}
	if version != valueVersion {
		return Value{}, fmt.Errorf("%w: version %d", ErrInvalidValue, version)
	}
	compressionByte, err := reader.ReadByte()
	if err != nil {
		return Value{}, ErrInvalidValue
	}
	compression := CompressionType(compressionByte)
	if !compression.Valid() {
		return Value{}, ErrUnsupportedCompression
	}
	timestamp, err := readInt64(reader)
	if err != nil {
		return Value{}, err
	}
	message, err := readBytes(reader)
	if err != nil {
		return Value{}, err
	}
	if reader.Len() != 0 {
		return Value{}, ErrInvalidValue
	}
	return Value{Timestamp: timestamp, Message: message, Compression: compression}, nil
}

func compressMessage(message []byte, compression CompressionType) ([]byte, error) {
	if err := validateMessageSize(len(message)); err != nil {
		return nil, err
	}
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
	case CompressionSnappy:
		return snappy.Encode(nil, message), nil
	case CompressionLZ4:
		var buf bytes.Buffer
		writer := lz4.NewWriter(&buf)
		if _, err := writer.Write(message); err != nil {
			_ = writer.Close()
			return nil, err
		}
		if err := writer.Close(); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	case CompressionZstd:
		encoder, _, err := zstdCodecs()
		if err != nil {
			return nil, err
		}
		return encoder.EncodeAll(message, nil), nil
	default:
		return nil, ErrUnsupportedCompression
	}
}

func decompressMessage(message []byte, compression CompressionType) ([]byte, error) {
	switch compression {
	case CompressionNone:
		if err := validateMessageSize(len(message)); err != nil {
			return nil, err
		}
		out := make([]byte, len(message))
		copy(out, message)
		return out, nil
	case CompressionGzip:
		reader, err := gzip.NewReader(bytes.NewReader(message))
		if err != nil {
			return nil, err
		}
		defer reader.Close()
		return readLimitedMessage(reader)
	case CompressionSnappy:
		decodedLength, err := snappy.DecodedLen(message)
		if err != nil {
			return nil, err
		}
		if err := validateMessageSize(decodedLength); err != nil {
			return nil, err
		}
		return snappy.Decode(nil, message)
	case CompressionLZ4:
		return readLimitedMessage(lz4.NewReader(bytes.NewReader(message)))
	case CompressionZstd:
		_, decoder, err := zstdCodecs()
		if err != nil {
			return nil, err
		}
		decoded, err := decoder.DecodeAll(message, nil)
		if errors.Is(err, zstd.ErrDecoderSizeExceeded) {
			return nil, ErrValueTooLarge
		}
		return decoded, err
	default:
		return nil, ErrUnsupportedCompression
	}
}

func validateMessageSize(size int) error {
	if size < 0 || size > MaxValueMessageSize {
		return ErrValueTooLarge
	}
	return nil
}

func readLimitedMessage(reader io.Reader) ([]byte, error) {
	message, err := io.ReadAll(io.LimitReader(reader, int64(MaxValueMessageSize)+1))
	if err != nil {
		return nil, err
	}
	if err := validateMessageSize(len(message)); err != nil {
		return nil, err
	}
	return message, nil
}

// zstdCodecs 延迟创建可并发复用的无状态 Zstd 编解码器。
// 编码选择 SpeedFastest，避免结构化日志写入因高压缩级别产生明显 CPU 抖动。
func zstdCodecs() (*zstd.Encoder, *zstd.Decoder, error) {
	zstdOnce.Do(func() {
		zstdEncoder, zstdInitErr = zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedFastest))
		if zstdInitErr != nil {
			return
		}
		zstdDecoder, zstdInitErr = zstd.NewReader(nil,
			zstd.WithDecoderMaxMemory(uint64(MaxValueMessageSize)),
		)
	})
	return zstdEncoder, zstdDecoder, zstdInitErr
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

func readBytes(r *bytes.Reader) ([]byte, error) {
	length, err := readUint32(r)
	if err != nil {
		return nil, err
	}
	if uint64(length) > uint64(r.Len()) {
		return nil, ErrInvalidValue
	}
	data := make([]byte, int(length))
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, ErrInvalidValue
	}
	return data, nil
}
