package main

import (
	"fmt"
	"sync"
	"time"

	"github.com/23jdd/SamKv/pkg/wal"
)

func main() {
	
	
	w, err := wal.New("logs")
	if err != nil {
		panic(err)
	}
	var wg sync.WaitGroup
	for i := range 100 {
		wg.Go(func() {
			temp := i
			w.AppendLog([]byte(fmt.Sprint("hello ", temp, "\n")))
		})
	}
	wg.Wait()
	time.Sleep(1 * time.Second)
}
