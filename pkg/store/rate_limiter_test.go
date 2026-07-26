package store

// 本文件验证限速 Writer 按 Burst 分块、精确计费，并透传等待错误。

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"testing"
)

type recordingLimiter struct {
	burst int
	calls []int
	err   error
}

func (l *recordingLimiter) Burst() int { return l.burst }

func (l *recordingLimiter) WaitN(_ context.Context, count int) error {
	l.calls = append(l.calls, count)
	return l.err
}

type shortWriter struct {
	output bytes.Buffer
}

func (w *shortWriter) Write(p []byte) (int, error) {
	return w.output.Write(p[:1])
}
func TestRateLimitedWriterSplitsAndAccountsBytes(t *testing.T) {
	limiter := &recordingLimiter{burst: 3}
	var output bytes.Buffer
	writer := newRateLimitedWriter(context.Background(), &output, limiter)
	written, err := writer.Write([]byte("abcdefgh"))
	if err != nil {
		t.Fatal(err)
	}
	if written != 8 || output.String() != "abcdefgh" {
		t.Fatalf("Write() = %d, %q", written, output.String())
	}
	if !reflect.DeepEqual(limiter.calls, []int{3, 3, 2}) {
		t.Fatalf("limiter calls = %v, want [3 3 2]", limiter.calls)
	}
}

func TestRateLimitedWriterReturnsWaitErrorBeforeWrite(t *testing.T) {
	wantErr := errors.New("cancelled")
	limiter := &recordingLimiter{burst: 4, err: wantErr}
	var output bytes.Buffer
	writer := newRateLimitedWriter(context.Background(), &output, limiter)
	if written, err := writer.Write([]byte("data")); written != 0 || !errors.Is(err, wantErr) {
		t.Fatalf("Write() = %d, %v", written, err)
	}
	if output.Len() != 0 {
		t.Fatalf("output length = %d, want 0", output.Len())
	}
}

func TestNewCompactionRateLimiterBoundaries(t *testing.T) {
	if limiter := newCompactionRateLimiter(0); limiter != nil {
		t.Fatal("zero rate should disable limiter")
	}
	limiter := newCompactionRateLimiter(DefaultCompactionRateLimitBytesPerSec)
	if limiter == nil || limiter.Burst() != maxCompactionRateBurst {
		t.Fatalf("default limiter = %#v", limiter)
	}
}

func TestRateLimitedWriterDoesNotChargeShortWritesTwice(t *testing.T) {
	limiter := &recordingLimiter{burst: 3}
	output := &shortWriter{}
	writer := newRateLimitedWriter(context.Background(), output, limiter)
	written, err := writer.Write([]byte("abcd"))
	if err != nil || written != 4 || output.output.String() != "abcd" {
		t.Fatalf("Write() = %d, %v, output %q", written, err, output.output.String())
	}
	if !reflect.DeepEqual(limiter.calls, []int{3, 1}) {
		t.Fatalf("limiter calls = %v, want [3 1]", limiter.calls)
	}
}
