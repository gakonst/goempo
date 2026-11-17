package statetest_test

import (
	"path/filepath"
	"testing"

	"github.com/gakonst/goempo/internal/statetest"
	"github.com/kapetan-io/tiger/assert"
)

func TestVectors(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "*.json"))
	assert.NoError(err, "glob testdata")
	assert.Ok(len(files) >= 5, "testdata holds the vectors")
	report := &statetest.Report{}
	for i := 0; i < len(files); i++ {
		outcomes, err := statetest.RunFile(files[i])
		assert.NoError(err, files[i])
		report.Add(outcomes)
	}
	for i := 0; i < len(report.Failures); i++ {
		t.Errorf("%s [%s #%d]: %s", report.Failures[i].Case.Name, report.Failures[i].Case.Fork, report.Failures[i].Case.Index, report.Failures[i].Reason)
	}
	passed := 0
	for i := 0; i < len(report.Forks); i++ {
		passed += report.Forks[i].Passed
	}
	assert.Ok(passed >= 9, "every supported case passes")
}
