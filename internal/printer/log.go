package printer

import (
	"context"
	"log"
)

// Log is the dry run into the log: each job is logged as a quoted string, so
// its bytes, CP1251 and \r\n included, can be read back exactly.
type Log struct {
	Logger *log.Logger
}

func (l Log) Print(_ context.Context, job []byte) error {
	l.Logger.Printf("dry run: %d bytes: %q", len(job), job)
	return nil
}
