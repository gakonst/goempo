package version_test

import (
	"testing"

	"github.com/gakonst/goempo/internal/version"
)

func TestString(t *testing.T) {
	if version.String() != "goempo 0.0.1" {
		t.Fatalf("got %q", version.String())
	}
}
