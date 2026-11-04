//go:build !windows

package printer

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// QueueExists is whether CUPS has a queue with this name, in which it ignores
// case.
func QueueExists(ctx context.Context, c Computer, name string) (bool, error) {
	names, err := queueNames(ctx, c)
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(names, func(n string) bool { return strings.EqualFold(n, name) }), nil
}

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
		exists, listErr := QueueExists(ctx, q.c, q.Name)
		if errors.Is(listErr, errSchedulerDown) {
			return fmt.Errorf("queue %s: %w", q.Name, listErr)
		}
		if listErr == nil && !exists {
			return fmt.Errorf("queue %s: %w", q.Name, ErrNotFound)
		}
	}
	return fmt.Errorf("queue %s: %w", q.Name, err)
}
