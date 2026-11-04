//go:build !windows

package printer

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Queue sends jobs to a CUPS queue with lp -o raw, so that no filter or
// driver touches the bytes.
type Queue struct {
	Name string
	c    Computer
}

// NewQueue is the queue with this name on the computer.
func NewQueue(name string, c Computer) Queue { return Queue{Name: name, c: c} }

func (q Queue) Print(ctx context.Context, job []byte) error {
	_, err := q.c.Run(ctx, job, "lp", "-d", q.Name, "-o", "raw", "-t", "printpipe job")
	if err == nil {
		return nil
	}
	// lp says the queue does not exist with the scheduler down too, so whether
	// it does is asked of the list of queues instead.
	if ctx.Err() == nil {
		names, listErr := queueNames(ctx, q.c)
		sameName := func(name string) bool { return strings.EqualFold(name, q.Name) }
		if errors.Is(listErr, errSchedulerDown) {
			return fmt.Errorf("queue %s: %w", q.Name, listErr)
		}
		if listErr == nil && !slices.ContainsFunc(names, sameName) {
			return fmt.Errorf("queue %s: %w", q.Name, ErrNotFound)
		}
	}
	return fmt.Errorf("queue %s: %w", q.Name, err)
}
