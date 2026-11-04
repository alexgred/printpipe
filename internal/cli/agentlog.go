package cli

import (
	"context"
	"io"
	"log"

	"github.com/alexgred/printpipe/internal/server"
)

// logCap is about how large the agent's log grows before it becomes the one
// previous file, path + ".1".
const logCap = 1 << 20

// agentLog is the agent's own log file. It never fails: a log that cannot be
// written must not stop a label from printing, so what cannot be written is
// lost. A log.Logger serializes its writes.
type agentLog struct {
	sys  System
	path string
	file io.WriteCloser
	size int64
}

func (l *agentLog) Write(p []byte) (int, error) {
	if l.file == nil && !l.open() {
		return len(p), nil
	}
	if l.size > 0 && l.size+int64(len(p)) > logCap {
		l.Close()
		if l.sys.Rename(l.path, l.path+".1") != nil || !l.open() {
			return len(p), nil
		}
	}
	n, _ := l.file.Write(p)
	l.size += int64(n)
	return len(p), nil
}

func (l *agentLog) open() bool {
	file, size, err := l.sys.OpenAppend(l.path)
	if err != nil {
		return false
	}
	l.file, l.size = file, size
	return true
}

// Close closes the file; a later Write opens it again.
func (l *agentLog) Close() {
	if l.file != nil {
		l.file.Close()
		l.file = nil
	}
}

// loggedPrinter adds a job that did not print to the log: the page gets the
// error, the log keeps it for whoever looks later.
type loggedPrinter struct {
	server.Printer
	logger *log.Logger
}

func (p loggedPrinter) Print(ctx context.Context, job []byte) error {
	err := p.Printer.Print(ctx, job)
	if err != nil {
		p.logger.Printf("print failed: %v", err)
	}
	return err
}
