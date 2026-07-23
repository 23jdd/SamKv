// Package parse 解析 SamKV 的结构化日志查询表达式。
package parse

import (
	"errors"
	"fmt"
	"time"

	"github.com/alecthomas/participle/v2"
	"github.com/alecthomas/participle/v2/lexer"
)

var (
	// ErrInvalidQueryFormat 表示查询表达式存在语法或语义错误。
	ErrInvalidQueryFormat = errors.New("parse: invalid query format")

	queryLexer = lexer.MustSimple([]lexer.SimpleRule{
		{Name: "Whitespace", Pattern: `\s+`},
		{Name: "String", Pattern: `"(?:\\.|[^"\\])*"`},
		{Name: "Duration", Pattern: `(?:[0-9]+(?:\.[0-9]+)?(?:ns|us|µs|μs|ms|s|m|h))+`},
		{Name: "Ident", Pattern: `[A-Za-z_][A-Za-z0-9_.:/-]*`},
		{Name: "Number", Pattern: `[0-9]+(?:\.[0-9]+)?`},
		{Name: "Punct", Pattern: `[{}\[\],=]`},
	})

	queryParser = participle.MustBuild[QueryFormat](
		participle.Lexer(queryLexer),
		participle.Elide("Whitespace"),
		participle.Unquote("String"),
		participle.UseLookahead(2),
	)
)

// Duration 是 QueryFormat 中可以由 time.ParseDuration 解析的持续时间。
type Duration time.Duration

// Capture 实现 participle.Capture，将 Duration token 转换为持续时间。
func (d *Duration) Capture(values []string) error {
	if len(values) != 1 {
		return fmt.Errorf("duration expects one token, got %d", len(values))
	}
	parsed, err := time.ParseDuration(values[0])
	if err != nil {
		return err
	}
	*d = Duration(parsed)
	return nil
}

// Value 返回标准库 time.Duration 值。
func (d Duration) Value() time.Duration {
	return time.Duration(d)
}

// String 返回 time.Duration 的规范字符串形式。
func (d Duration) String() string {
	return time.Duration(d).String()
}

// LabelMatcher 表示一个标签等值条件，例如 app=nginx。
type LabelMatcher struct {
	Name  string `parser:"@Ident '='"`
	Value string `parser:"@(String | Ident | Number)"`
}

// QueryFormat 是 query{labels}[range] offset duration 查询的语法树。
// offset 是可选部分，省略时为 0。
type QueryFormat struct {
	Labels []LabelMatcher `parser:"'query' '{' ( @@ ( ',' @@ )* )? '}'"`
	Range  Duration       `parser:"'[' @Duration ']'"`
	Offset Duration       `parser:"( 'offset' @Duration )?"`
}

// ParseQueryFormat 使用 Participle 解析并校验一条完整查询表达式。
func ParseQueryFormat(input string) (*QueryFormat, error) {
	query, err := queryParser.ParseString("query", input)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidQueryFormat, err)
	}
	if err := query.validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidQueryFormat, err)
	}
	return query, nil
}

// TimeRange 根据基准时间计算查询实际覆盖的闭区间。
// offset 会先将结束时间向过去移动，再减去 range 得到开始时间。
func (q QueryFormat) TimeRange(now time.Time) (start, end time.Time) {
	end = now.Add(-q.Offset.Value())
	start = end.Add(-q.Range.Value())
	return start, end
}

func (q QueryFormat) validate() error {
	if q.Range.Value() <= 0 {
		return errors.New("range must be greater than zero")
	}
	if q.Offset.Value() < 0 {
		return errors.New("offset must not be negative")
	}

	seen := make(map[string]struct{}, len(q.Labels))
	for _, label := range q.Labels {
		if _, ok := seen[label.Name]; ok {
			return fmt.Errorf("duplicate label %q", label.Name)
		}
		seen[label.Name] = struct{}{}
	}
	return nil
}
