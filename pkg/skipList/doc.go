// Package skiplist 提供泛型、有序且并发安全的内存跳表。
//
// 调用者通过 CompareFunc 定义键的全序关系，再使用 Add、Set、Get、Delete 和
// LowerBound 完成点操作。Range 与 Entries 返回按比较器排序的快照，适合 MemTable
// 刷盘或范围查询。
//
// 所有公开方法都由内部 RWMutex 保护。Range 会先在读锁内复制快照，再释放锁执行
// 回调，因此回调可以再次调用 Set/Delete，但看不到遍历开始后的修改。键和值本身若为
// 指针或包含可变引用，其指向对象仍由调用者负责同步。
//
// 边界条件：比较器不能为空且必须满足稳定的全序；最大层数必须为正，晋升概率必须位于
// (0,1)。Add 遇到重复键不会覆盖，Set 才会覆盖。空表查询返回对应类型零值和 false。
package skiplist
