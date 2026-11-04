package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The usblp driver hands a non-blocking write over and cancels what the
// printer has not taken yet on close, so a label is lost with no error. A FIFO
// stands in for the device: like it, Go would make it non-blocking when opened
// with os.OpenFile, and it stops taking data when nothing reads it.
func TestWriteDeviceBlocksUntilTheDeviceTakesTheJobOrTheDeadlinePasses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lp0")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	reader, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	job := make([]byte, 1<<20) // more than a pipe holds
	result := make(chan error, 1)
	go func() { result <- OS{}.WriteDevice(ctx, path, job) }()

	flags := writerFlags(t, path)
	if flags&syscall.O_NONBLOCK != 0 {
		t.Errorf("device open with flags %o: non-blocking", flags)
	}
	if err := <-result; !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("got %v, want the deadline passing", err)
	}
	// Let the write left behind finish, so that it ends with the test.
	go func() { _, _ = io.Copy(io.Discard, reader) }()
}

// writerFlags is the open flags of this process's write-only descriptor of
// path, waiting for it to be opened.
func writerFlags(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(900 * time.Millisecond)
	for time.Now().Before(deadline) {
		fds, err := os.ReadDir("/proc/self/fd")
		if err != nil {
			t.Fatal(err)
		}
		for _, fd := range fds {
			if target, _ := os.Readlink("/proc/self/fd/" + fd.Name()); target != path {
				continue
			}
			info, err := os.ReadFile("/proc/self/fdinfo/" + fd.Name())
			if err != nil {
				continue
			}
			for line := range strings.Lines(string(info)) {
				if value, ok := strings.CutPrefix(line, "flags:"); ok {
					flags, err := strconv.ParseInt(strings.TrimSpace(value), 8, 64)
					if err == nil && int(flags)&syscall.O_ACCMODE == syscall.O_WRONLY {
						return int(flags)
					}
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the device was not opened for writing")
	return 0
}
