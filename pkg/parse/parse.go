// Package parse 使用 Participle 解析 SamKV 的结构化日志查询表达式。
//
// 完整语法为 matcher{label=value,...}[range] offset duration，其中 offset 可省略。
// matcher、标签值可以是标识符、数字或双引号字符串；包含空格、emoji 或标点的内容应
// 使用双引号。标签采用等值匹配，空集合 {} 合法。
//
// ParseQueryFormat 会同时完成语法与语义校验：matcher 不能为空、range 必须大于 0、
// offset 不能为负、同名标签不能重复。TimeRange 以调用方传入的 now 为基准计算窗口，
// 不读取系统时间，因此查询层应统一传入 UTC 时间以避免展示上的时区歧义。
package parse

// 本文件定义 Participle 词法规则、查询 AST、Duration 捕获和语义校验。
// 解析只负责结构，不执行 matcher 通配符或日志内容过滤。

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
		// 未加引号的标识符支持 Unicode 字母/数字；emoji 等符号应使用 String token。
		{Name: "Ident", Pattern: `[\p{L}_][\p{L}\p{N}_.:/-]*`},
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

// Capture 实现 participle.Capture，将一个 Duration token 转换为持续时间。
// Participle 正常只传入一个 token；数量不是 1 或 time.ParseDuration 失败时返回错误。
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
	// Name 必须是未加引号的 Ident，且同一 QueryFormat 内不能重复。
	Name string `parser:"@Ident '='"`
	// Value 可为引号字符串、标识符或数字，解析后不包含外围双引号。
	Value string `parser:"@(String | Ident | Number)"`
}

// QueryFormat 是 matcher{labels}[range] offset duration 查询的语法树。
// Query 保存日志内容匹配字符串；offset 是可选部分，省略时为 0。
type QueryFormat struct {
	// Query 是内容 matcher；通配符语义由查询执行层解释。
	Query string `parser:"@(String | Ident | Number)"`
	// Labels 是零个或多个标签等值条件。
	Labels []LabelMatcher `parser:"'{' ( @@ ( ',' @@ )* )? '}'"`
	// Range 是查询窗口长度，必须大于 0。
	Range Duration `parser:"'[' @Duration ']'"`
	// Offset 把窗口整体向过去平移，省略时为 0。
	Offset Duration `parser:"( 'offset' @Duration )?"`
}

// ParseQueryFormat 使用 Participle 解析并校验一条完整查询表达式。
// input 必须包含 matcher、花括号和正数时间范围；空白可出现在 token 之间。
// 返回错误可用 errors.Is(err, ErrInvalidQueryFormat) 判断，具体原因保留在包装错误中。
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

// TimeRange 根据基准时间计算查询实际覆盖窗口。
// offset 会先将结束时间向过去移动，再减去 range 得到开始时间；存储扫描通常使用 [start,end)。
// 方法假定 QueryFormat 已通过 ParseQueryFormat 校验，手工构造的非法负时长不会再次检查。
func (q QueryFormat) TimeRange(now time.Time) (start, end time.Time) {
	end = now.Add(-q.Offset.Value())
	start = end.Add(-q.Range.Value())
	return start, end
}

func (q QueryFormat) validate() error {
	if q.Query == "" {
		return errors.New("query matcher must not be empty")
	}
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
