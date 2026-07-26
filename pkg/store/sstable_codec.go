package store

// 本文件提供 SSTable 各编码器共享的完整写入和小端整数辅助函数。
// 所有调用方必须传播错误，不能把底层短写当作成功发布。

import (
	"encoding/binary"
	"io"
)

// writeAll 保证 data 被完整写入，避免短写被当作成功。
func writeAll(w io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := w.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}

// writeUint32 以小端序写入 uint32。
func writeUint32(w io.Writer, v uint32) error {
	var buf [4]byte
	binary.LittleEndian.PutUint32(buf[:], v)
	_, err := w.Write(buf[:])
	return err
}

// writeUint64 以小端序写入 uint64。
func writeUint64(w io.Writer, v uint64) error {
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], v)
	_, err := w.Write(buf[:])
	return err
}
