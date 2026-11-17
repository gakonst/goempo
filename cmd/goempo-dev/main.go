package main

import (
	"fmt"
	"os"

	"github.com/gakonst/goempo/internal/version"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: goempo-dev <fetch|replay|score|statetest|genesis|follow>")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "statetest":
		os.Exit(runStatetest(os.Args[2:]))
	case "genesis":
		os.Exit(runGenesis(os.Args[2:]))
	case "fetch":
		os.Exit(runFetch(os.Args[2:]))
	case "fetch-range":
		os.Exit(runFetchRange(os.Args[2:]))
	case "follow":
		os.Exit(runFollow(os.Args[2:]))
	case "replay":
		os.Exit(runReplay(os.Args[2:]))
	case "score":
		os.Exit(runScore(os.Args[2:]))
	}
	fmt.Fprintln(os.Stderr, version.String()+": unknown command "+os.Args[1])
	os.Exit(2)
}
