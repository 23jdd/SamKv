package skiplist

import (
	"math/rand"
	"sync"
	"time"
)

const (
	defaultMaxLevel    = 20
	defaultProbability = 0.25
)

// CompareFunc 定义 Key 的比较方式。
//
// 返回值：
//   < 0：a < b
//   = 0：a == b
//   > 0：a > b
type CompareFunc[K any] func(a, b K) int

type Node[K any, V any] struct {
	Key   K
	Value V

	// 不对外暴露，避免调用者修改 SkipList 内部结构。
	forward []*Node[K, V]
}

// Height 返回节点高度。
func (n *Node[K, V]) Height() int {
	if n == nil {
		return 0
	}
	return len(n.forward)
}

type Entry[K any, V any] struct {
	Key   K
	Value V
}

type SkipList[K any, V any] struct {
	mu sync.RWMutex

	head *Node[K, V]

	// 当前实际使用的层数，最少为 1。
	level int

	maxLevel   int
	probability float64

	length int

	compare CompareFunc[K]
	random  *rand.Rand
}

// New 创建一个 SkipList。
//
// compare 不能为 nil。
func New[K any, V any](compare CompareFunc[K]) *SkipList[K, V] {
	return NewWithConfig[K, V](
		compare,
		defaultMaxLevel,
		defaultProbability,
	)
}

// NewWithConfig 创建一个可以自定义最大层数和晋升概率的 SkipList。
func NewWithConfig[K any, V any](
	compare CompareFunc[K],
	maxLevel int,
	probability float64,
) *SkipList[K, V] {
	if compare == nil {
		panic("skiplist: compare function cannot be nil")
	}

	if maxLevel <= 0 {
		panic("skiplist: maxLevel must be greater than 0")
	}

	if probability <= 0 || probability >= 1 {
		panic("skiplist: probability must be between 0 and 1")
	}

	return &SkipList[K, V]{
		head: &Node[K, V]{
			forward: make([]*Node[K, V], maxLevel),
		},
		level:       1,
		maxLevel:    maxLevel,
		probability: probability,
		compare:     compare,
		random: rand.New(
			rand.NewSource(time.Now().UnixNano()),
		),
	}
}

// Len 返回元素数量。
func (s *SkipList[K, V]) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.length
}

// IsEmpty 判断 SkipList 是否为空。
func (s *SkipList[K, V]) IsEmpty() bool {
	return s.Len() == 0
}

// Add 插入一个新元素。
//
// 如果 Key 已存在，不更新 Value，并返回 false。
// 如果插入成功，返回 true。
func (s *SkipList[K, V]) Add(key K, value V) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, exists := s.insert(key, value, false)
	return !exists
}

// Set 插入或更新元素。
//
// 返回值：
//   oldValue：Key 原来对应的 Value
//   replaced：是否替换了已有元素
//
// 如果 Key 不存在，会插入新节点，replaced 为 false。
func (s *SkipList[K, V]) Set(key K, value V) (
	oldValue V,
	replaced bool,
) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.insert(key, value, true)
}

// insert 执行实际插入逻辑。
//
// exists 表示 Key 在插入前是否已经存在。
// 调用者必须持有写锁。
func (s *SkipList[K, V]) insert(
	key K,
	value V,
	replace bool,
) (oldValue V, exists bool) {
	update := make([]*Node[K, V], s.maxLevel)

	current := s.head

	// 从最高层向下查找每一层的前驱节点。
	for level := s.level - 1; level >= 0; level-- {
		for {
			next := current.forward[level]

			if next == nil {
				break
			}

			if s.compare(next.Key, key) >= 0 {
				break
			}

			current = next
		}

		update[level] = current
	}

	// 第 0 层包含所有节点。
	current = current.forward[0]

	// Key 已经存在。
	if current != nil && s.compare(current.Key, key) == 0 {
		oldValue = current.Value

		if replace {
			current.Value = value
		}

		return oldValue, true
	}

	nodeLevel := s.randomLevel()

	// 新节点高度超过当前 SkipList 高度。
	if nodeLevel > s.level {
		for level := s.level; level < nodeLevel; level++ {
			update[level] = s.head
		}

		s.level = nodeLevel
	}

	node := &Node[K, V]{
		Key:     key,
		Value:   value,
		forward: make([]*Node[K, V], nodeLevel),
	}

	// 将新节点插入每一层。
	for level := 0; level < nodeLevel; level++ {
		node.forward[level] = update[level].forward[level]
		update[level].forward[level] = node
	}

	s.length++

	return oldValue, false
}

// Get 根据 Key 查询 Value。
func (s *SkipList[K, V]) Get(key K) (V, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	node := s.findNode(key)
	if node == nil {
		var zero V
		return zero, false
	}

	return node.Value, true
}

// Contains 判断指定 Key 是否存在。
func (s *SkipList[K, V]) Contains(key K) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.findNode(key) != nil
}

// findNode 查找完全匹配的节点。
// 调用者必须至少持有读锁。
func (s *SkipList[K, V]) findNode(key K) *Node[K, V] {
	current := s.head

	for level := s.level - 1; level >= 0; level-- {
		for {
			next := current.forward[level]

			if next == nil {
				break
			}

			result := s.compare(next.Key, key)

			if result >= 0 {
				break
			}

			current = next
		}
	}

	current = current.forward[0]

	if current != nil && s.compare(current.Key, key) == 0 {
		return current
	}

	return nil
}

// Delete 删除指定 Key。
//
// 返回被删除的 Value，以及是否成功删除。
func (s *SkipList[K, V]) Delete(key K) (V, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	update := make([]*Node[K, V], s.maxLevel)
	current := s.head

	for level := s.level - 1; level >= 0; level-- {
		for {
			next := current.forward[level]

			if next == nil {
				break
			}

			if s.compare(next.Key, key) >= 0 {
				break
			}

			current = next
		}

		update[level] = current
	}

	target := current.forward[0]

	if target == nil || s.compare(target.Key, key) != 0 {
		var zero V
		return zero, false
	}

	for level := 0; level < s.level; level++ {
		// target 的高度可能没有这么高。
		if update[level].forward[level] != target {
			break
		}

		update[level].forward[level] = target.forward[level]
	}

	// 如果最高层已经没有节点，降低 SkipList 层数。
	for s.level > 1 && s.head.forward[s.level-1] == nil {
		s.level--
	}

	s.length--

	value := target.Value

	// 解除引用，便于 GC。
	for i := range target.forward {
		target.forward[i] = nil
	}

	return value, true
}

// LowerBound 查找第一个 Key >= target 的元素。
//
// 这里的 >= 由 compare 函数定义。
func (s *SkipList[K, V]) LowerBound(
	target K,
) (key K, value V, found bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	current := s.head

	for level := s.level - 1; level >= 0; level-- {
		for {
			next := current.forward[level]

			if next == nil {
				break
			}

			if s.compare(next.Key, target) >= 0 {
				break
			}

			current = next
		}
	}

	current = current.forward[0]

	if current == nil {
		return key, value, false
	}

	return current.Key, current.Value, true
}

// First 返回排序后的第一个元素。
func (s *SkipList[K, V]) First() (
	key K,
	value V,
	found bool,
) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	first := s.head.forward[0]
	if first == nil {
		return key, value, false
	}

	return first.Key, first.Value, true
}

// Last 返回排序后的最后一个元素。
func (s *SkipList[K, V]) Last() (
	key K,
	value V,
	found bool,
) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.length == 0 {
		return key, value, false
	}

	current := s.head

	for level := s.level - 1; level >= 0; level-- {
		for current.forward[level] != nil {
			current = current.forward[level]
		}
	}

	return current.Key, current.Value, true
}

// Range 按 Key 排序顺序遍历所有元素。
//
// fn 返回 false 时停止遍历。
//
// 为了避免 fn 内部调用 Set/Delete 时发生死锁，
// 这里先复制快照，然后释放锁，再执行回调。
func (s *SkipList[K, V]) Range(
	fn func(key K, value V) bool,
) {
	if fn == nil {
		return
	}

	s.mu.RLock()

	entries := make([]Entry[K, V], 0, s.length)

	current := s.head.forward[0]
	for current != nil {
		entries = append(entries, Entry[K, V]{
			Key:   current.Key,
			Value: current.Value,
		})

		current = current.forward[0]
	}

	s.mu.RUnlock()

	for _, entry := range entries {
		if !fn(entry.Key, entry.Value) {
			return
		}
	}
}

// Entries 返回所有元素的有序快照。
func (s *SkipList[K, V]) Entries() []Entry[K, V] {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries := make([]Entry[K, V], 0, s.length)

	current := s.head.forward[0]
	for current != nil {
		entries = append(entries, Entry[K, V]{
			Key:   current.Key,
			Value: current.Value,
		})

		current = current.forward[0]
	}

	return entries
}

// Clear 清空 SkipList。
func (s *SkipList[K, V]) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.head.forward {
		s.head.forward[i] = nil
	}

	s.level = 1
	s.length = 0
}

// randomLevel 随机生成节点高度。
// 返回范围为 [1, maxLevel]。
//
// 调用者必须持有写锁，因为 rand.Rand 本身不是并发安全的。
func (s *SkipList[K, V]) randomLevel() int {
	level := 1

	for level < s.maxLevel &&
		s.random.Float64() < s.probability {
		level++
	}

	return level
}