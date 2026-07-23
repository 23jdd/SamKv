package main

import (
	"fmt"

	"github.com/23jdd/SamKv/pkg/store"
)

func main() {

	st, err := store.NewStoreManger("logs", 10)
	if err != nil {
		panic(err)
	}
	st.ReLoad()
	val,_:= st.Get("hello")	
	fmt.Println(val)
}
