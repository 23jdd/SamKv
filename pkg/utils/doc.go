// Package utils 定义结构化日志使用的复合 Key 和压缩 Value 二进制格式。
//
// Key 按“时间戳、规范化标签、序列号”编码，二进制字典序与时间戳和序列号顺序一致。
// 标签在编码前按 Name、Value 排序，因此调用方传入顺序不会改变结果。相同时间戳和标签
// 下必须分配不同序列号，才能避免 KV 覆盖。
//
// Value 只保存时间戳、压缩算法和日志正文；标签已经进入 Key，不应重复写入 Value。
// MarshalBinary/UnmarshalValue 负责磁盘格式，DecompressedMessage 才返回原始正文。
//
// 边界条件：标签名不能为空，名称和值不能包含 NUL；竖线、等号和百分号会自动转义。
// DecodeKey/UnmarshalValue 只接受完整编码，截断、尾随数据、未知版本或压缩算法都会报错。
// EncodeKeyString 只用于展示，不具备二进制 Key 的固定宽度和排序承诺。
package utils
