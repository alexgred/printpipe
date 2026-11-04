package printer

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// USBID is a config device naming a USB printer, usb:VID:PID[:serial] with
// the IDs in hex. Without a serial it is any one connected printer with
// these IDs, so that a different USB port or a replaced unit of the same
// model just works.
type USBID struct {
	VID, PID uint16
	// Serial is the USB serial number; empty matches any.
	Serial string
}

// ParseUSB reads a config device usb:VID:PID[:serial].
func ParseUSB(device string) (USBID, error) {
	bad := func(why string) (USBID, error) {
		return USBID{}, fmt.Errorf("device %q: %s; want usb:VID:PID or usb:VID:PID:serial, such as usb:1FC9:2016", device, why)
	}
	rest, ok := strings.CutPrefix(device, "usb:")
	if !ok {
		return bad("not a USB device")
	}
	parts := strings.Split(rest, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return bad("not two or three parts")
	}
	vid, err1 := strconv.ParseUint(parts[0], 16, 16)
	pid, err2 := strconv.ParseUint(parts[1], 16, 16)
	if err1 != nil || err2 != nil {
		return bad("VID and PID are hex numbers of up to 4 digits")
	}
	id := USBID{VID: uint16(vid), PID: uint16(pid)}
	if len(parts) == 3 {
		if parts[2] == "" {
			return bad("empty serial")
		}
		id.Serial = parts[2]
	}
	return id, nil
}

// String is the config device: usb:1FC9:2016 or usb:1FC9:2016:serial.
func (id USBID) String() string {
	s := fmt.Sprintf("usb:%04X:%04X", id.VID, id.PID)
	if id.Serial != "" {
		s += ":" + id.Serial
	}
	return s
}

// USB writes jobs straight to a USB printer, found again by its IDs on every
// job. Only Windows has it: there it is the usbprint device interface,
// written without a driver, a queue or admin rights.
type USB struct {
	ID USBID
	// bus is the computer's USB printers; nil is the real one.
	bus bus
}

// USBPrinter is a connected USB printer.
type USBPrinter struct {
	// USBID is its IDs and USB serial number (upper case; empty when it has
	// none), the config device naming exactly this printer.
	USBID
	// Name is its IEEE 1284 manufacturer and model, such as
	// "Xprinter XP-365B"; empty when it could not be read.
	Name string
	// path is the OS's name for it, such as the device interface path.
	path string
}

// matches is whether the printer p is one the device id names: the same IDs,
// and the same serial when id has one.
func (id USBID) matches(p USBID) bool {
	return p.VID == id.VID && p.PID == id.PID && (id.Serial == "" || strings.EqualFold(p.Serial, id.Serial))
}

// String is how the CLI lists it: 1FC9:2016 "Xprinter XP-365B".
func (p USBPrinter) String() string {
	return fmt.Sprintf("%04X:%04X %q", p.VID, p.PID, p.Name)
}

// AmbiguousError is several connected printers matching the configured
// device; Printers lets the CLI offer a serial-specific device for each.
type AmbiguousError struct {
	ID       USBID
	Printers []USBPrinter
}

func (e *AmbiguousError) Error() string {
	ids := make([]string, len(e.Printers))
	for i, p := range e.Printers {
		ids[i] = p.USBID.String()
		if p.Serial == "" {
			ids[i] += " (no serial)"
		}
	}
	return fmt.Sprintf("printer %s: %d printers match: %s", e.ID, len(e.Printers), strings.Join(ids, ", "))
}

func (e *AmbiguousError) Is(target error) bool { return target == ErrAmbiguous }

// bus is the OS side of USB printing.
type bus interface {
	// paths are the OS's names of the connected USB printers.
	paths() ([]string, error)
	// deviceID is the IEEE 1284 device ID the printer at path reports, ""
	// when it cannot be read, such as while another program holds it.
	deviceID(path string) string
	// write sends the whole job to the printer at path by ctx's deadline,
	// failing with the OS's own error.
	write(ctx context.Context, path string, job []byte) error
}

// writeTimeout is how long a job may take to be written before the printer
// counts as not responding.
const writeTimeout = 10 * time.Second

// writing is held while a job is written to a printer directly.
var writing = make(chan struct{}, 1)

// startWrite waits for the turn to write to a printer directly and sets the
// write's deadline; done ends it. One job at a time: the device is opened
// exclusively, so a second job written meanwhile would fail as busy instead of
// following.
func startWrite(ctx context.Context) (_ context.Context, done func(), _ error) {
	select {
	case writing <- struct{}{}:
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	}
	ctx, cancel := context.WithTimeout(ctx, writeTimeout)
	return ctx, func() { cancel(); <-writing }, nil
}

func (u USB) Print(ctx context.Context, job []byte) error {
	b := u.bus
	if b == nil {
		b = osBus{}
	}
	printers, err := connectedUSB(b)
	if err != nil {
		return fmt.Errorf("printer %s: %w", u.ID, err)
	}
	var match []USBPrinter
	for _, p := range printers {
		if u.ID.matches(p.USBID) {
			match = append(match, p)
		}
	}
	switch len(match) {
	case 0:
		return fmt.Errorf("printer %s: %w", u.ID, ErrNotFound)
	case 1:
	default:
		return &AmbiguousError{ID: u.ID, Printers: match}
	}
	ctx, done, err := startWrite(ctx)
	if err != nil {
		return fmt.Errorf("printer %s: %w", u.ID, err)
	}
	defer done()
	if err := b.write(ctx, match[0].path, job); err != nil {
		if cause := writeCause(err); cause != nil {
			return fmt.Errorf("printer %s: %w: %w", u.ID, cause, err)
		}
		return fmt.Errorf("printer %s: %w", u.ID, err)
	}
	return nil
}

// The Win32 errors a write to a usbprint device fails with that a page can
// act on. They are numbers here, so that their meaning is tested on any OS.
const (
	errorFileNotFound       syscall.Errno = 2
	errorPathNotFound       syscall.Errno = 3
	errorSharingViolation   syscall.Errno = 32
	errorBusy               syscall.Errno = 170
	errorWaitTimeout        syscall.Errno = 258 // the write did not finish by the deadline and was cancelled
	errorNoSuchDevice       syscall.Errno = 433
	errorOperationAborted   syscall.Errno = 995
	errorDeviceNotConnected syscall.Errno = 1167
)

// writeCause is the failure a page can act on behind a write's OS error, or
// nil when there is none.
func writeCause(err error) error {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return nil
	}
	switch errno {
	case errorSharingViolation, errorBusy:
		return ErrBusy
	case errorWaitTimeout, errorOperationAborted:
		return ErrNotResponding
	case errorFileNotFound, errorPathNotFound, errorNoSuchDevice, errorDeviceNotConnected:
		// Unplugged between being found and being written to.
		return ErrNotFound
	}
	return nil
}

func namedUSB(b bus) ([]USBPrinter, error) {
	printers, err := connectedUSB(b)
	for i, p := range printers {
		printers[i].Name = name1284(b.deviceID(p.path))
	}
	return printers, err
}

// connectedUSB is the connected USB printers, without their names: reading
// a name opens the printer.
func connectedUSB(b bus) ([]USBPrinter, error) {
	paths, err := b.paths()
	if err != nil {
		return nil, err
	}
	var printers []USBPrinter
	for _, path := range paths {
		if p, ok := parseInterfacePath(path); ok {
			printers = append(printers, p)
		}
	}
	return printers, nil
}

// parseInterfacePath reads the IDs and serial out of a Windows device
// interface path, \\?\usb#vid_1fc9&pid_2016#<serial>#{guid}. The printer
// function of a composite device (vid_…&pid_…&mi_00) has no serial there,
// and neither has a device without one: Windows makes up an instance ID with
// & in it instead.
func parseInterfacePath(path string) (USBPrinter, bool) {
	parts := strings.Split(path, "#")
	if len(parts) < 3 {
		return USBPrinter{}, false
	}
	p := USBPrinter{path: path}
	var haveVID, havePID bool
	for field := range strings.SplitSeq(strings.ToLower(parts[1]), "&") {
		key, value, _ := strings.Cut(field, "_")
		n, err := strconv.ParseUint(value, 16, 16)
		switch {
		case err != nil:
		case key == "vid":
			p.VID, haveVID = uint16(n), true
		case key == "pid":
			p.PID, havePID = uint16(n), true
		}
	}
	if !haveVID || !havePID {
		return USBPrinter{}, false
	}
	if !strings.Contains(strings.ToLower(parts[1]), "&mi_") && !strings.Contains(parts[2], "&") {
		p.Serial = strings.ToUpper(parts[2])
	}
	return p, true
}

// name1284 is the manufacturer and model an IEEE 1284 device ID names, such
// as "Xprinter XP-365B" for MFG:Xprinter;MDL:XP-365B;.
func name1284(id string) string {
	var mfg, mdl string
	for field := range strings.SplitSeq(id, ";") {
		key, value, _ := strings.Cut(field, ":")
		switch strings.ToUpper(strings.TrimSpace(key)) {
		case "MFG", "MANUFACTURER":
			mfg = strings.TrimSpace(value)
		case "MDL", "MODEL":
			mdl = strings.TrimSpace(value)
		}
	}
	return strings.TrimSpace(mfg + " " + mdl)
}
