package printer

import (
	"context"
	"fmt"
	"os"
)

// Device writes jobs straight to the printer's device file, such as
// /dev/usb/lp0 on Linux: the fallback when there is no CUPS queue.
type Device struct {
	Path string
}

func (d Device) Print(_ context.Context, job []byte) error {
	f, err := os.OpenFile(d.Path, os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("printer %s: %w", d.Path, err)
	}
	if _, err := f.Write(job); err != nil {
		f.Close()
		return fmt.Errorf("printer %s: %w", d.Path, err)
	}
	return f.Close()
}
