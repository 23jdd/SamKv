package parse_test

// 本文件展示 QueryFormat 从文本到时间窗口的完整用法。
// 固定 now 可以让 Example 输出在任意时区和运行时间下保持稳定。

import (
	"fmt"
	"time"

	"github.com/23jdd/SamKv/pkg/parse"
)

func ExampleParseQueryFormat() {
	query, err := parse.ParseQueryFormat(`"request failed"{app=api,level=ERROR}[5m] offset 1h`)
	if err != nil {
		panic(err)
	}
	now := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	start, end := query.TimeRange(now)

	fmt.Println(query.Query, len(query.Labels))
	fmt.Println(start.Format(time.RFC3339), end.Format(time.RFC3339))

	// Output:
	// request failed 2
	// 2024-01-01T10:55:00Z 2024-01-01T11:00:00Z
}
