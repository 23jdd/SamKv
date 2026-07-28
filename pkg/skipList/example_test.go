package skiplist_test

import (
	"fmt"

	skiplist "github.com/23jdd/SamKv/pkg/skipList"
)

func ExampleSkipList() {
	list := skiplist.New[int, string](func(a, b int) int { return a - b })
	list.Add(20, "twenty")
	list.Add(10, "ten")
	old, replaced := list.Append(20, "TWENTY")
	fmt.Println(old, replaced)

	key, value, found := list.LowerBound(15)
	fmt.Println(key, value, found)
	for _, entry := range list.Entries() {
		fmt.Printf("%d=%s ", entry.Key, entry.Value)
	}

	// Output:
	// twenty true
	// 20 twenty true
	// 10=ten 20=twenty
}
