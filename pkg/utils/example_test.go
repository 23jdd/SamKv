package utils_test

// 本文件给出复合 Key 与 Value 的完整编码、解码流程。
// Example 使用 CompressionNone 让输出稳定；生产日志可使用默认 gzip 压缩。

import (
	"fmt"

	"github.com/23jdd/SamKv/pkg/utils"
)

func ExampleEncodeKey() {
	key, err := utils.EncodeKey(1704103200000000000, []utils.Label{
		{Name: "level", Value: "ERROR"},
		{Name: "app", Value: "nginx"},
	}, 7)
	if err != nil {
		panic(err)
	}
	decoded, err := utils.DecodeKey(key)
	if err != nil {
		panic(err)
	}
	fmt.Println(decoded.Timestamp, decoded.Labels, decoded.Sequence)

	// Output:
	// 1704103200000000000 [{app nginx} {level ERROR}] 7
}

func ExampleValue() {
	value, err := utils.NewValueWithCompression(1704103200000000000, []byte("request failed"), utils.CompressionNone)
	if err != nil {
		panic(err)
	}
	encoded, err := value.MarshalBinary()
	if err != nil {
		panic(err)
	}
	decoded, err := utils.UnmarshalValue(encoded)
	if err != nil {
		panic(err)
	}
	message, err := decoded.DecompressedMessage()
	if err != nil {
		panic(err)
	}
	fmt.Println(decoded.Timestamp, string(message))

	// Output:
	// 1704103200000000000 request failed
}
