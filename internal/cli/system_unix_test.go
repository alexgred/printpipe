//go:build !windows

package cli

import (
	"errors"
	"testing"
)

// The first file lsof names as a process's text is its program; the libraries
// come after it.
func TestLsofProgramIsTheFirstTextFile(t *testing.T) {
	out := "p4242\nftxt\nn/Users/me/my tools/printpipe\nftxt\nn/usr/lib/dyld\n"

	got, err := lsofProgram(out)
	if err != nil || got != "/Users/me/my tools/printpipe" {
		t.Errorf("got %q, %v", got, err)
	}
}

func TestLsofProgramFailsWhenLsofNamesNoFile(t *testing.T) {
	if got, err := lsofProgram("p4242\n"); !errors.Is(err, errNoProgram) {
		t.Errorf("got %q, %v", got, err)
	}
}
