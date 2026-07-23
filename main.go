package main

import (
	"fmt"

	"github.com/23jdd/SamKv/pkg/store"
)

func main() {

     m := store.Magic
     fmt.Println([]byte(m))
}
