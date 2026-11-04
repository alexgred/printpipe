//go:build !windows

package cli

import (
	"errors"
	"os"
	"path/filepath"
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

// A file left by a failed try has whatever mode it has; what is written must run.
func TestWriteProgramMakesARunnableFileOverALeftover(t *testing.T) {
	name := filepath.Join(t.TempDir(), "bin", ".printpipe.new")
	for range 2 {
		if err := (OS{}).WriteProgram(name, []byte("program")); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(name)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0o100 == 0 {
			t.Errorf("mode %v, want it runnable", info.Mode())
		}
		if err := os.Chmod(name, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := os.ReadFile(name); string(got) != "program" {
		t.Errorf("the file has %q", got)
	}
}
