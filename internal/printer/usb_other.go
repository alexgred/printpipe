//go:build !windows

package printer

import (
	"context"
	"errors"
)

// ListUSB is the connected USB printers with their names, known models or
// not, found without root, such as for install to find a known one.
func ListUSB(ctx context.Context, c Computer) ([]USBPrinter, error) {
	if c.GOOS() == "darwin" {
		return ioregUSB(ctx, c)
	}
	return sysfsUSB(c)
}

// errUSBWindowsOnly: on macOS and Linux a USB printer is reached through a
// CUPS queue or a device file instead.
var errUSBWindowsOnly = errors.New("usb: devices are printed to only on Windows; use a queue, or a device file such as /dev/usb/lp0")

// osBus has no USB printers of its own outside Windows.
type osBus struct{}

func (osBus) paths() ([]string, error)                    { return nil, errUSBWindowsOnly }
func (osBus) deviceID(string) string                      { return "" }
func (osBus) write(context.Context, string, []byte) error { return errUSBWindowsOnly }
