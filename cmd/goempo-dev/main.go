package main

import (
	"fmt"
	"os"

	"github.com/gakonst/goempo/internal/version"
)

func main() { os.Exit(Run(os.Args[1:])) }

func Run(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: goempo-dev <fetch|replay|score|statetest|genesis|follow|certify|rpcdiff|serve>")
		return 2
	}
	switch args[0] {
	case "statetest":
		return runStatetest(args[1:])
	case "genesis":
		return runGenesis(args[1:])
	case "fetch":
		return runFetch(args[1:])
	case "fetch-range":
		return runFetchRange(args[1:])
	case "certify":
		return runCertify(args[1:])
	case "follow":
		return runFollow(args[1:])
	case "replay":
		return runReplay(args[1:])
	case "serve":
		return runServe(args[1:])
	case "rpcdiff":
		return runRPCDiff(args[1:])
	case "score":
		return runScore(args[1:])
	}
	fmt.Fprintln(os.Stderr, version.String()+": unknown command "+args[0])
	return 2
}
