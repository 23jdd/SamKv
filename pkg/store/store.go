package store

import (
	"os"
	"path/filepath"

	"github.com/23jdd/SamKv/pkg/wal"
)

type StoreManger struct {
	mem *MemTable
	wm  *wal.WalManger
}

func NewStoreManger(dir string, limit int) (*StoreManger, error) {
	wm, err := wal.New(dir)
	if err != nil {
		return nil, err
	}
	return &StoreManger{mem: NewMemTable(limit), wm: wm}, nil
}

func (st *StoreManger) Put(key string, val string) error {
	err := st.wm.AppendRecord(wal.PutRecord([]byte(key), []byte(val)))
	if err != nil {
		return err
	}
	st.mem.Put(key, val)
	return nil
}
func (st *StoreManger) Get(key string) (string, bool) {
	return st.mem.Get(key)
}
func (st *StoreManger) Delete(key string) error {
	err := st.wm.AppendRecord(wal.DeleteRecord([]byte(key)))
	if err != nil {
		return err
	}
	st.mem.Delete(key)
	return nil
}
func (st *StoreManger) ReLoad() {
	path := filepath.Join(st.wm.Dir, "wal.log")
	reader, err := os.Open(path)
	if err != nil {
		panic(err)
	}
	Recover(reader,st.mem)
}
