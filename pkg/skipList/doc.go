// Package skiplist 提供泛型、有序且并发安全的无锁内存跳表。
//
// 调用者通过 CompareFunc 定义键的全序关系，再使用 Add、Append、Get 和
// LowerBound 完成点操作。Range 与 Entries 返回按比较器排序的快照，适合 MemTable
// 刷盘或范围查询。
//
// 插入基于 CAS 实现无锁并发，查询过程完全无锁。节点一旦插入即不可修改（Immutable），
// 不支持删除。专为 LSM Tree 的 MemTable 场景设计。
//
// 边界条件：比较器不能为空且必须满足稳定的全序；最大层数必须为正，晋升概率必须位于
// (0,1)。Add 遇到重复键不会覆盖，Append 遇到重复键返回已存在的旧值但不覆盖。
// 空表查询返回对应类型零值和 false。
package skiplist
