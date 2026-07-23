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
	if err := st.wm.AppendRecord(wal.PutRecord([]byte(key), []byte(val))); err != nil {
		return err
	}
	return st.mem.Put(key, val)
}

func (st *StoreManger) Get(key string) (string, bool) {
	return st.mem.Get(key)
}

func (st *StoreManger) Delete(key string) error {
	if err := st.wm.AppendRecord(wal.DeleteRecord([]byte(key))); err != nil {
		return err
	}
	return st.mem.Delete(key)
}

func (st *StoreManger) ReLoad() {
	path := filepath.Join(st.wm.Dir, "wal.log")
	reader, err := os.Open(path)
	if err != nil {
		panic(err)
	}
	defer reader.Close()
	_ = Recover(reader, st.mem)
}
