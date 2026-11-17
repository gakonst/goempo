package statetest

import (
	"maps"
	"slices"
)

func RunFile(path string) ([]Outcome, error) {
	fixtures, err := LoadFile(path)
	if err != nil {
		return nil, err
	}
	var out []Outcome
	for _, name := range slices.Sorted(maps.Keys(fixtures)) {
		fx := fixtures[name]
		out = append(out, RunFixture(name, &fx)...)
	}
	return out, nil
}

type ForkTally struct {
	Fork    string
	Passed  int
	Failed  int
	Skipped int
}

type Report struct {
	Forks    []ForkTally
	Failures []Outcome
	Errors   []string
}

func (r *Report) Add(outcomes []Outcome) {
	for i := 0; i < len(outcomes); i++ {
		o := outcomes[i]
		t := r.tally(o.Case.Fork)
		switch {
		case o.Skipped:
			t.Skipped++
		case o.Passed:
			t.Passed++
		default:
			t.Failed++
			r.Failures = append(r.Failures, o)
		}
	}
}

func (r *Report) tally(fork string) *ForkTally {
	for i := 0; i < len(r.Forks); i++ {
		if r.Forks[i].Fork == fork {
			return &r.Forks[i]
		}
	}
	r.Forks = append(r.Forks, ForkTally{Fork: fork})
	return &r.Forks[len(r.Forks)-1]
}
