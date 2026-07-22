package store

import (
	skiplist "github.com/23jdd/SamKv/pkg/skipList"
)

type MemTable struct {
	table *skiplist.SkipList[string, string] // 键为 []byte，值为 *Value
	size  int                                // 当前数据大小
	limit int                                // 触发刷盘的阈值
	mutable bool   //   
}

func Compare(a string, b string) int {
	if a > b {
		return 1
	} else if a == b {
		return 0
	} else {
		return -1
	}
}
func NewMemTable(limit int) *MemTable {
	return &MemTable{table: skiplist.New[string, string](Compare), limit: limit,mutable: true}
}
func (mt *MemTable) Get(key string)(string,bool){
          return mt.table.Get(key)
}
func (mt*MemTable)Put(key string,value string){
	  mt.table.Add(key,value)
}
func (mt*MemTable)Delete(key string){
	     mt.table.Delete(key)
}