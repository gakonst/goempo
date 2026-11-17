package main

import (
	"fmt"
	"os"

	"github.com/gakonst/goempo/internal/version"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		fmt.Println(version.String())
		return
	}
	fmt.Fprintln(os.Stderr, "usage: tempo node [flags]")
	os.Exit(2)
}
