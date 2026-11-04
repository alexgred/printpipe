package printer

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"syscall"
)

// Device writes jobs straight to the printer's device file, such as
// /dev/usb/lp0 on Linux: the fallback when there is no CUPS queue.
type Device struct {
	Path     string
	Computer Computer
}

func (d Device) Print(ctx context.Context, job []byte) error {
	ctx, done, err := startWrite(ctx)
	if err != nil {
		return fmt.Errorf("printer %s: %w", d.Path, err)
	}
	defer done()
	err = d.Computer.WriteDevice(ctx, d.Path, job)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, fs.ErrPermission):
		return fmt.Errorf("%s%s", noPermission, d.Path)
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, syscall.ENODEV):
		return fmt.Errorf("printer %s: %w", d.Path, ErrNotFound)
	case errors.Is(err, syscall.EBUSY):
		return fmt.Errorf("printer %s: %w", d.Path, ErrBusy)
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("printer %s: %w", d.Path, ErrNotResponding)
	default:
		return fmt.Errorf("printer %s: %w", d.Path, err)
	}
}

// noPermission starts the message of a device file the agent may not write
// to. It has no /print code of its own, so the message is how the CLI tells it
// from other failures.
const noPermission = "no permission to write to "

// IsNoPermission is whether a /print message says the agent may not write to
// the device file.
func IsNoPermission(message string) bool { return strings.HasPrefix(message, noPermission) }
