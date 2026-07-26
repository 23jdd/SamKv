package store

// 本文件把 golang.org/x/time/rate 令牌桶适配为 SSTable 顺序写入器。
// Compaction 的多个输出共享同一 limiter，因此配置限制的是聚合写带宽。

import (
	"context"
	"io"

	"golang.org/x/time/rate"
)

const maxCompactionRateBurst = 1024 * 1024

type byteRateLimiter interface {
	Burst() int
	WaitN(context.Context, int) error
}

// newCompactionRateLimiter 根据每秒字节数创建令牌桶；0 表示关闭限速。
// Burst 最大为 1 MiB，避免高限速配置在空闲后一次放行过大的磁盘突发。
func newCompactionRateLimiter(bytesPerSecond int64) *rate.Limiter {
	if bytesPerSecond <= 0 {
		return nil
	}
	burst := bytesPerSecond
	if burst > maxCompactionRateBurst {
		burst = maxCompactionRateBurst
	}
	return rate.NewLimiter(rate.Limit(bytesPerSecond), int(burst))
}

type rateLimitedWriter struct {
	ctx     context.Context
	writer  io.Writer
	limiter byteRateLimiter
}

// newRateLimitedWriter 包装 writer；limiter=nil 时直接返回原 writer，不增加热路径开销。
func newRateLimitedWriter(ctx context.Context, writer io.Writer, limiter byteRateLimiter) io.Writer {
	if limiter == nil {
		return writer
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return &rateLimitedWriter{ctx: ctx, writer: writer, limiter: limiter}
}

// Write 在每个分块写入前消费等量 token。
// limiter 的 Burst 必须大于 0；底层短写由调用方的 writeAll 继续处理。
func (w *rateLimitedWriter) Write(p []byte) (int, error) {
	burst := w.limiter.Burst()
	if burst <= 0 {
		return 0, ErrInvalidOptions
	}
	total := 0
	for len(p) > 0 {
		chunkSize := len(p)
		if chunkSize > burst {
			chunkSize = burst
		}
		if err := w.limiter.WaitN(w.ctx, chunkSize); err != nil {
			return total, err
		}
		chunk := p[:chunkSize]
		for len(chunk) > 0 {
			n, err := w.writer.Write(chunk)
			total += n
			chunk = chunk[n:]
			if err != nil {
				return total, err
			}
			if n == 0 {
				return total, io.ErrShortWrite
			}
		}
		p = p[chunkSize:]
	}
	return total, nil
}
