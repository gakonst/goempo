package main

import (
	"fmt"
	"os"

	"github.com/gakonst/goempo/internal/version"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: goempo-dev <fetch|replay|score|statetest>")
		os.Exit(2)
	}
	fmt.Fprintln(os.Stderr, version.String()+": unknown command "+os.Args[1])
	os.Exit(2)
}
