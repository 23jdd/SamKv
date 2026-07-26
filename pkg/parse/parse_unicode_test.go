package parse

import (
	"errors"
	"testing"
	"time"
)

func TestParseQueryFormatWithUnicode(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    *QueryFormat
		wantErr bool
	}{
		// 基础英文
		{
			name:  "basic english",
			input: `error{app=nginx,level=warn}[5m]`,
			want: &QueryFormat{
				Query: "error",
				Labels: []LabelMatcher{
					{Name: "app", Value: "nginx"},
					{Name: "level", Value: "warn"},
				},
				Range:  Duration(5 * time.Minute),
				Offset: Duration(0),
			},
		},
		// 中文 Query 裸词
		{
			name:  "chinese query bare",
			input: `查询{标签=值}[1h]`,
			want: &QueryFormat{
				Query: "查询",
				Labels: []LabelMatcher{
					{Name: "标签", Value: "值"},
				},
				Range:  Duration(1 * time.Hour),
				Offset: Duration(0),
			},
		},
		// 中文标签名和值
		{
			name:  "chinese labels",
			input: `日志{服务名=用户服务,环境=生产}[30m] offset 1h`,
			want: &QueryFormat{
				Query: "日志",
				Labels: []LabelMatcher{
					{Name: "服务名", Value: "用户服务"},
					{Name: "环境", Value: "生产"},
				},
				Range:  Duration(30 * time.Minute),
				Offset: Duration(1 * time.Hour),
			},
		},
		// 中英文混合
		{
			name:  "mixed cjk and ascii",
			input: `error日志{应用=nginx,级别=error}[10m]`,
			want: &QueryFormat{
				Query: "error日志",
				Labels: []LabelMatcher{
					{Name: "应用", Value: "nginx"},
					{Name: "级别", Value: "error"},
				},
				Range: Duration(10 * time.Minute),
			},
		},
		// 引号字符串含中文
		{
			name:  "quoted chinese",
			input: `"查询 词"{标签="中文 值"}[1h]`,
			want: &QueryFormat{
				Query: "查询 词",
				Labels: []LabelMatcher{
					{Name: "标签", Value: "中文 值"},
				},
				Range: Duration(1 * time.Hour),
			},
		},
		// 纯数字 Query
		{
			name:  "numeric query",
			input: `404{app=nginx}[1m]`,
			want: &QueryFormat{
				Query: "404",
				Labels: []LabelMatcher{
					{Name: "app", Value: "nginx"},
				},
				Range: Duration(1 * time.Minute),
			},
		},
		// 无标签
		{
			name:  "no labels",
			input: `query{}[5m]`,
			want: &QueryFormat{
				Query:  "query",
				Labels: nil,
				Range:  Duration(5 * time.Minute),
			},
		},
		// 带 path 的 Ident
		{
			name:  "ident with colon slash",
			input: `http:GET/app{status=200}[1h]`,
			want: &QueryFormat{
				Query: "http:GET/app",
				Labels: []LabelMatcher{
					{Name: "status", Value: "200"},
				},
				Range: Duration(1 * time.Hour),
			},
		},
		// 错误：空字符串
		{
			name:    "empty input",
			input:   ``,
			wantErr: true,
		},
		// 错误：缺少花括号
		{
			name:    "missing braces",
			input:   `query[5m]`,
			wantErr: true,
		},
		// 错误：range 为零
		{
			name:    "zero range",
			input:   `query{}[0s]`,
			wantErr: true,
		},
		// 错误：重复标签
		{
			name:    "duplicate labels",
			input:   `query{a=1,a=2}[5m]`,
			wantErr: true,
		},
		// 错误：负 offset（语法层面即失败，lexer 不支持负号 Duration）
		{
			name:    "negative offset syntax",
			input:   `query{}[5m] offset -1h`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseQueryFormat(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseQueryFormat(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if tt.wantErr {
				if !errors.Is(err, ErrInvalidQueryFormat) {
					t.Fatalf("expected wrapped ErrInvalidQueryFormat, got: %v", err)
				}
				return
			}

			if got.Query != tt.want.Query {
				t.Errorf("Query = %q, want %q", got.Query, tt.want.Query)
			}
			if len(got.Labels) != len(tt.want.Labels) {
				t.Fatalf("Labels length = %d, want %d", len(got.Labels), len(tt.want.Labels))
			}
			for i := range got.Labels {
				if got.Labels[i] != tt.want.Labels[i] {
					t.Errorf("Labels[%d] = %+v, want %+v", i, got.Labels[i], tt.want.Labels[i])
				}
			}
			if got.Range != tt.want.Range {
				t.Errorf("Range = %v, want %v", got.Range, tt.want.Range)
			}
			if got.Offset != tt.want.Offset {
				t.Errorf("Offset = %v, want %v", got.Offset, tt.want.Offset)
			}
		})
	}
}

func TestQueryFormat_TimeRange(t *testing.T) {
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name   string
		q      QueryFormat
		start  time.Time
		end    time.Time
	}{
		{
			name: "no offset",
			q: QueryFormat{
				Range: Duration(1 * time.Hour),
			},
			end:   now,
			start: now.Add(-1 * time.Hour),
		},
		{
			name: "with offset",
			q: QueryFormat{
				Range:  Duration(30 * time.Minute),
				Offset: Duration(1 * time.Hour),
			},
			end:   now.Add(-1 * time.Hour),
			start: now.Add(-1*time.Hour - 30*time.Minute),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start, end := tt.q.TimeRange(now)
			if !start.Equal(tt.start) {
				t.Errorf("start = %v, want %v", start, tt.start)
			}
			if !end.Equal(tt.end) {
				t.Errorf("end = %v, want %v", end, tt.end)
			}
		})
	}
}