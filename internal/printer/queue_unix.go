//go:build !windows

package printer

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Queue sends jobs to a CUPS queue with lp -o raw, so that no filter or
// driver touches the bytes.
type Queue struct {
	Name string
}

func (q Queue) Print(ctx context.Context, job []byte) error {
	cmd := exec.CommandContext(ctx, "lp", "-d", q.Name, "-o", "raw", "-t", "printpipe job")
	cmd.Stdin = bytes.NewReader(job)
	if out, err := cmd.CombinedOutput(); err != nil {
		// lp's message is localized, so whether the queue exists is asked of
		// lpstat's exit status instead.
		if exec.CommandContext(ctx, "lpstat", "-p", q.Name).Run() != nil && ctx.Err() == nil {
			return fmt.Errorf("queue %s: %w", q.Name, ErrNotFound)
		}
		return fmt.Errorf("queue %s: %w: %s", q.Name, err, strings.TrimSpace(string(out)))
	}
	return nil
}
