package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// result is what a user sees of one command.
type result struct {
	stdout, stderr string
	code           int
}

func run(t *testing.T, sys System, args ...string) result {
	t.Helper()
	return runCtx(context.Background(), sys, args...)
}

func runCtx(ctx context.Context, sys System, args ...string) result {
	var stdout, stderr bytes.Buffer
	code := Run(ctx, args, &stdout, &stderr, sys)
	return result{stdout.String(), stderr.String(), code}
}

func TestVersionPrintsTheVersion(t *testing.T) {
	got := run(t, newFakeSystem(), "version")
	if got.code != 0 || got.stdout != "printpipe dev\n" || got.stderr != "" {
		t.Errorf("got %+v, want printpipe dev on stdout and exit 0", got)
	}
}

func TestHelpListsTheCommands(t *testing.T) {
	for _, args := range [][]string{{"help"}, {}, {"--help"}, {"-h"}} {
		got := run(t, newFakeSystem(), args...)
		if got.code != 0 || got.stderr != "" {
			t.Fatalf("%q: got %+v, want exit 0 and nothing on stderr", args, got)
		}
		for _, cmd := range []string{"help", "start", "stop", "restart", "startup [on|off]", "config", "version", "install", "upgrade", "remove [--queue]", "status", "test-print [--size"} {
			if !strings.Contains(got.stdout, "\n  "+cmd) {
				t.Errorf("%q: help does not list %q:\n%s", args, cmd, got.stdout)
			}
		}
		if strings.Contains(got.stdout, "serve") {
			t.Errorf("%q: help lists the hidden server command:\n%s", args, got.stdout)
		}
	}
}

func TestWrongUsageExitsWithTwo(t *testing.T) {
	for _, tc := range []struct {
		args  []string
		error string
	}{
		{[]string{"print"}, `error: unknown command "print"`},
		{[]string{"version", "--json"}, "error: unknown flag --json"},
		{[]string{"version", "now"}, `error: unexpected argument "now"`},
		{[]string{"serve", "--config"}, "error: flag --config needs a value"},
		{[]string{"serve", "--dry-run"}, "error: flag --dry-run needs a value"},
		{[]string{"serve", "--port", "1"}, "error: unknown flag --port"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			got := run(t, newFakeSystem(), tc.args...)
			if got.code != 2 || got.stdout != "" {
				t.Errorf("got %+v, want exit 2 and nothing on stdout", got)
			}
			lines := strings.Split(strings.TrimSuffix(got.stderr, "\n"), "\n")
			if lines[0] != tc.error {
				t.Errorf("stderr starts with %q, want %q", lines[0], tc.error)
			}
			if len(lines) != 2 || lines[1] != "  Run printpipe help to see the commands." {
				t.Errorf("stderr %q, want the error and the hint to run printpipe help", got.stderr)
			}
		})
	}
}
