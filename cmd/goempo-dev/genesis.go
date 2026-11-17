package main

import (
	"fmt"
	"os"

	"github.com/gakonst/goempo/internal/genesis"
	"github.com/gakonst/goempo/internal/primitives"
)

func runGenesis(args []string) int {
	g, err := loadGenesis(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "genesis:", err)
		return 2
	}
	expected := genesis.MainnetStateRoot
	if len(args) >= 2 {
		if expected, err = primitives.HexToHash(args[1]); err != nil {
			fmt.Fprintln(os.Stderr, "genesis: expected root:", err)
			return 2
		}
	}
	computed := g.StateRoot()
	fmt.Printf("chain_id %d\naccounts %d\ncomputed %s\nexpected %s\n", g.Config.ChainID, len(g.Alloc), computed, expected)
	if computed != expected {
		fmt.Println("R2 FAIL")
		return 1
	}
	fmt.Println("R2 PASS")
	return 0
}

func loadGenesis(args []string) (*genesis.Genesis, error) {
	if len(args) == 0 {
		return genesis.Mainnet()
	}
	raw, err := os.ReadFile(args[0])
	if err != nil {
		return nil, err
	}
	return genesis.Parse(raw)
}
