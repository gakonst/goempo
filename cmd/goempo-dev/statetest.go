package main

import (
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/gakonst/goempo/internal/statetest"
)

const statetestFilesMax = 1 << 20

const _ = uint(statetestFilesMax - 1)

func runStatetest(args []string) int {
	set := flag.NewFlagSet("statetest", flag.ContinueOnError)
	fork := set.String("fork", "", "only count cases of this fork (e.g. Osaka)")
	show := set.Int("show", 20, "failures to print")
	err := set.Parse(args)
	if err != nil {
		return 2
	}
	files, err := collectFixtures(set.Args())
	if err != nil {
		fmt.Fprintln(os.Stderr, "statetest:", err)
		return 2
	}
	if len(files) > statetestFilesMax {
		fmt.Fprintln(os.Stderr, "statetest: too many fixture files")
		return 2
	}
	report := &statetest.Report{}
	for i := 0; i < len(files); i++ {
		runOne(report, files[i])
	}
	return printReport(report, reportView{fork: *fork, show: min(max(*show, 0), statetestFilesMax)})
}

func runOne(report *statetest.Report, path string) {
	outcomes, err := statetest.RunFile(path)
	if err != nil {
		report.Errors = append(report.Errors, path+": "+err.Error())
		return
	}
	report.Add(outcomes)
}

func collectFixtures(roots []string) ([]string, error) {
	var files []string
	walk := func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".json") {
			files = append(files, path)
		}
		return nil
	}
	for i := 0; i < len(roots); i++ {
		err := filepath.WalkDir(roots[i], walk)
		if err != nil {
			return nil, err
		}
	}
	slices.Sort(files)
	return files, nil
}

type reportView struct {
	fork string
	show int
}

func printReport(r *statetest.Report, view reportView) int {
	passed, failed := 0, 0
	for i := 0; i < len(r.Forks); i++ {
		t := r.Forks[i]
		if view.fork != "" && t.Fork != view.fork {
			continue
		}
		passed += t.Passed
		failed += t.Failed
		fmt.Printf("%-18s pass %6d  fail %6d  skip %6d\n", t.Fork, t.Passed, t.Failed, t.Skipped)
	}
	shown := 0
	for i := 0; i < len(r.Failures) && shown < view.show; i++ {
		f := r.Failures[i]
		if view.fork != "" && f.Case.Fork != view.fork {
			continue
		}
		shown++
		fmt.Printf("FAIL %s [%s #%d]: %s\n", f.Case.Name, f.Case.Fork, f.Case.Index, f.Reason)
	}
	for i := 0; i < len(r.Errors); i++ {
		fmt.Println("ERROR", r.Errors[i])
	}
	total := passed + failed
	if total == 0 {
		fmt.Println("no cases")
		return 1
	}
	fmt.Printf("total pass %d / %d (%.2f%%)\n", passed, total, 100*float64(passed)/float64(total))
	if failed > 0 {
		return 1
	}
	return 0
}
