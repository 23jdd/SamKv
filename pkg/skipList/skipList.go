package skiplist

import (
	"math/rand"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultMaxLevel    = 20
	defaultProbability = 0.25
)

type CompareFunc[K any] func(a, b K) int

type Node[K any, V any] struct {
	Key   K
	Value V

	forward []atomic.Pointer[Node[K, V]]
}

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
	head atomic.Pointer[Node[K, V]]

	level atomic.Int32

	maxLevel    int
	probability float64

	length atomic.Int64

	compare CompareFunc[K]
	random  *rand.Rand
	randMu  sync.Mutex
}

func New[K any, V any](compare CompareFunc[K]) *SkipList[K, V] {
	return NewWithConfig[K, V](
		compare,
		defaultMaxLevel,
		defaultProbability,
	)
}

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

	head := &Node[K, V]{
		forward: make([]atomic.Pointer[Node[K, V]], maxLevel),
	}

	sl := &SkipList[K, V]{
		maxLevel:    maxLevel,
		probability: probability,
		compare:     compare,
		random: rand.New(
			rand.NewSource(time.Now().UnixNano()),
		),
	}
	sl.head.Store(head)
	sl.level.Store(1)
	return sl
}

func (s *SkipList[K, V]) Len() int {
	return int(s.length.Load())
}

func (s *SkipList[K, V]) IsEmpty() bool {
	return s.Len() == 0
}

func (s *SkipList[K, V]) Add(key K, value V) bool {
	_, exists := s.insert(key, value, false)
	return !exists
}

func (s *SkipList[K, V]) Append(key K, value V) (
	oldValue V,
	replaced bool,
) {
	return s.insert(key, value, true)
}

func (s *SkipList[K, V]) insert(
	key K,
	value V,
	replace bool,
) (oldValue V, exists bool) {
	for {
		head := s.head.Load()
		currentLevel := int(s.level.Load())

		update := make([]*Node[K, V], s.maxLevel)
		succs := make([]*Node[K, V], s.maxLevel)

		current := head

		for level := currentLevel - 1; level >= 0; level-- {
			for {
				next := current.forward[level].Load()

				if next == nil {
					break
				}

				if s.compare(next.Key, key) >= 0 {
					break
				}

				current = next
			}

			update[level] = current
			succs[level] = current.forward[level].Load()
		}

		for level := currentLevel; level < s.maxLevel; level++ {
			update[level] = head
			succs[level] = nil
		}

		next0 := update[0].forward[0].Load()

		if next0 != nil && s.compare(next0.Key, key) == 0 {
			oldValue = next0.Value

			_ = replace

			return oldValue, true
		}

		nodeLevel := s.randomLevel()

		node := &Node[K, V]{
			Key:     key,
			Value:   value,
			forward: make([]atomic.Pointer[Node[K, V]], nodeLevel),
		}

		for level := 0; level < nodeLevel; level++ {
			node.forward[level].Store(succs[level])
		}

		if !update[0].forward[0].CompareAndSwap(succs[0], node) {
			continue
		}

		for level := 1; level < nodeLevel; level++ {
			for {
				pred := update[level]
				succ := succs[level]

				if pred.forward[level].Load() != succ {
					current := head
					for {
						next := current.forward[level].Load()
						if next == nil {
							break
						}
						if s.compare(next.Key, key) >= 0 {
							break
						}
						current = next
					}
					pred = current
					succ = current.forward[level].Load()
					update[level] = pred
					succs[level] = succ
				}

				if pred.forward[level].CompareAndSwap(succ, node) {
					break
				}

				succ = pred.forward[level].Load()
				succs[level] = succ
			}
		}

		for {
			oldLevel := s.level.Load()
			if int32(nodeLevel) <= oldLevel {
				break
			}
			if s.level.CompareAndSwap(oldLevel, int32(nodeLevel)) {
				break
			}
		}

		s.length.Add(1)

		return oldValue, false
	}
}

func (s *SkipList[K, V]) Get(key K) (V, bool) {
	node := s.findNode(key)
	if node == nil {
		var zero V
		return zero, false
	}

	return node.Value, true
}

func (s *SkipList[K, V]) Contains(key K) bool {
	return s.findNode(key) != nil
}

func (s *SkipList[K, V]) findNode(key K) *Node[K, V] {
	head := s.head.Load()
	current := head

	for level := int(s.level.Load()) - 1; level >= 0; level-- {
		for {
			next := current.forward[level].Load()

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

	current = current.forward[0].Load()

	if current != nil && s.compare(current.Key, key) == 0 {
		return current
	}

	return nil
}

func (s *SkipList[K, V]) LowerBound(
	target K,
) (key K, value V, found bool) {
	head := s.head.Load()
	current := head

	for level := int(s.level.Load()) - 1; level >= 0; level-- {
		for {
			next := current.forward[level].Load()

			if next == nil {
				break
			}

			if s.compare(next.Key, target) >= 0 {
				break
			}

			current = next
		}
	}

	current = current.forward[0].Load()

	if current == nil {
		return key, value, false
	}

	return current.Key, current.Value, true
}

func (s *SkipList[K, V]) First() (
	key K,
	value V,
	found bool,
) {
	head := s.head.Load()
	first := head.forward[0].Load()
	if first == nil {
		return key, value, false
	}

	return first.Key, first.Value, true
}

func (s *SkipList[K, V]) Last() (
	key K,
	value V,
	found bool,
) {
	if s.length.Load() == 0 {
		return key, value, false
	}

	head := s.head.Load()
	current := head

	for level := int(s.level.Load()) - 1; level >= 0; level-- {
		for current.forward[level].Load() != nil {
			current = current.forward[level].Load()
		}
	}

	if current == head {
		return key, value, false
	}

	return current.Key, current.Value, true
}

func (s *SkipList[K, V]) Range(
	fn func(key K, value V) bool,
) {
	if fn == nil {
		return
	}

	entries := s.Entries()

	for _, entry := range entries {
		if !fn(entry.Key, entry.Value) {
			return
		}
	}
}

func (s *SkipList[K, V]) Entries() []Entry[K, V] {
	head := s.head.Load()
	length := int(s.length.Load())

	entries := make([]Entry[K, V], 0, length)

	current := head.forward[0].Load()
	for current != nil {
		entries = append(entries, Entry[K, V]{
			Key:   current.Key,
			Value: current.Value,
		})

		current = current.forward[0].Load()
	}

	return entries
}

func (s *SkipList[K, V]) Clear() {
	newHead := &Node[K, V]{
		forward: make([]atomic.Pointer[Node[K, V]], s.maxLevel),
	}

	s.head.Store(newHead)
	s.level.Store(1)
	s.length.Store(0)
}

func (s *SkipList[K, V]) randomLevel() int {
	s.randMu.Lock()
	defer s.randMu.Unlock()

	level := 1

	for level < s.maxLevel &&
		s.random.Float64() < s.probability {
		level++
	}

	return level
}
