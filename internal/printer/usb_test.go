package printer

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestAUSBDeviceSettingIsParsed(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want USBID
	}{
		{"usb:1FC9:2016", USBID{VID: 0x1FC9, PID: 0x2016}},
		{"usb:1fc9:2016", USBID{VID: 0x1FC9, PID: 0x2016}},
		{"usb:1FC9:2016:A1B2C3", USBID{VID: 0x1FC9, PID: 0x2016, Serial: "A1B2C3"}},
	} {
		got, err := ParseUSB(tc.in)
		if err != nil || got != tc.want {
			t.Errorf("ParseUSB(%q) = %+v, %v; want %+v", tc.in, got, err, tc.want)
		}
	}
	if got := (USBID{VID: 0x1FC9, PID: 0x2016, Serial: "A1B2C3"}).String(); got != "usb:1FC9:2016:A1B2C3" {
		t.Errorf("String() = %q", got)
	}
	if got := (USBID{VID: 0x1FC9, PID: 0x2016}).String(); got != "usb:1FC9:2016" {
		t.Errorf("String() = %q", got)
	}
}

func TestABadUSBDeviceSettingIsRefused(t *testing.T) {
	for _, in := range []string{
		"", "/dev/usb/lp0", "usb:", "usb:1FC9", "usb:1FC9:", "usb::2016",
		"usb:xyz:2016", "usb:1FC9:20160", "usb:1FC9:2016:", "usb:+1:2016", "usb:1FC9:2016:A:B",
	} {
		if got, err := ParseUSB(in); err == nil {
			t.Errorf("ParseUSB(%q) = %+v, want an error", in, got)
		}
	}
}

// fakeBus is the computer's USB printers and what a write to each does.
type fakeBus struct {
	connected []busDevice
	// fails is the OS error a write to a path fails with.
	fails   map[string]error
	written map[string][]byte
	// deadline is the context deadline of the last write.
	deadline time.Time
}

// busDevice is a connected printer: its device interface path and IEEE 1284
// device ID.
type busDevice struct{ path, deviceID string }

func (b *fakeBus) paths() ([]string, error) {
	var paths []string
	for _, d := range b.connected {
		paths = append(paths, d.path)
	}
	return paths, nil
}

func (b *fakeBus) deviceID(path string) string {
	for _, d := range b.connected {
		if d.path == path {
			return d.deviceID
		}
	}
	return ""
}

func (b *fakeBus) write(ctx context.Context, path string, job []byte) error {
	b.deadline, _ = ctx.Deadline()
	if err := b.fails[path]; err != nil {
		return err
	}
	if b.written == nil {
		b.written = map[string][]byte{}
	}
	b.written[path] = job
	return nil
}

const (
	xp1 = `\\?\usb#vid_1fc9&pid_2016#a1b2c3#{28d78fad-5a12-11d1-ae5b-0000f803a8c2}`
	xp2 = `\\?\usb#vid_1fc9&pid_2016#d4e5f6#{28d78fad-5a12-11d1-ae5b-0000f803a8c2}`
	// A unit without a USB serial: Windows makes up an instance ID with &.
	xpNoSerial = `\\?\usb#vid_1fc9&pid_2016#5&2a3b4c5d&0&1#{28d78fad-5a12-11d1-ae5b-0000f803a8c2}`
	// The printer function of a composite device.
	other = `\\?\usb#vid_0483&pid_5720&mi_00#6&1a2b3c4d&0&0000#{28d78fad-5a12-11d1-ae5b-0000f803a8c2}`
)

const xpID = "MFG:Xprinter;CMD:TSPL;MDL:XP-365B;CLS:PRINTER;"

func TestAJobGoesToTheOnePrinterWithTheConfiguredIDs(t *testing.T) {
	bus := &fakeBus{connected: []busDevice{{other, "MFG:Acme;MDL:P1;"}, {xp1, xpID}}}
	p := USB{ID: USBID{VID: 0x1FC9, PID: 0x2016}, bus: bus}
	job := []byte("PRINT 1,1\r\n")
	if err := p.Print(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if got := bus.written[xp1]; !bytes.Equal(got, job) {
		t.Errorf("written to the XP-365B %q, want %q; all: %q", got, job, bus.written)
	}
	if len(bus.written) != 1 {
		t.Errorf("written to %d printers", len(bus.written))
	}
}

func TestASerialChoosesOneOfIdenticalPrinters(t *testing.T) {
	bus := &fakeBus{connected: []busDevice{{xp1, xpID}, {xp2, xpID}}}
	// Windows lowercases the serial in the path; the config may not.
	p := USB{ID: USBID{VID: 0x1FC9, PID: 0x2016, Serial: "D4E5F6"}, bus: bus}
	if err := p.Print(context.Background(), []byte("x")); err != nil {
		t.Fatal(err)
	}
	if _, ok := bus.written[xp2]; !ok || len(bus.written) != 1 {
		t.Errorf("written to %q, want only the second printer", bus.written)
	}
}

func TestNoMatchingPrinterIsNotFound(t *testing.T) {
	for name, id := range map[string]USBID{
		"other IDs":    {VID: 0x1FC9, PID: 0x2017},
		"other serial": {VID: 0x1FC9, PID: 0x2016, Serial: "ZZZ"},
	} {
		bus := &fakeBus{connected: []busDevice{{xp1, xpID}, {other, ""}}}
		err := USB{ID: id, bus: bus}.Print(context.Background(), []byte("x"))
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("%s: %v, want ErrNotFound", name, err)
		}
		if len(bus.written) != 0 {
			t.Errorf("%s: written to %q", name, bus.written)
		}
	}
}

// Two identical printers and no serial: printpipe does not guess, and says
// which serials there are to choose from.
func TestSeveralMatchingPrintersAreAmbiguous(t *testing.T) {
	bus := &fakeBus{connected: []busDevice{{xp1, xpID}, {xp2, xpID}, {xpNoSerial, xpID}}}
	err := USB{ID: USBID{VID: 0x1FC9, PID: 0x2016}, bus: bus}.Print(context.Background(), []byte("x"))
	if !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("%v, want ErrAmbiguous", err)
	}
	var amb *AmbiguousError
	if !errors.As(err, &amb) {
		t.Fatalf("%T is no *AmbiguousError", err)
	}
	var got []string
	for _, p := range amb.Printers {
		got = append(got, p.USBID.String())
	}
	if want := []string{"usb:1FC9:2016:A1B2C3", "usb:1FC9:2016:D4E5F6", "usb:1FC9:2016"}; !slices.Equal(got, want) {
		t.Errorf("printers %q, want %q", got, want)
	}
	for _, s := range []string{"usb:1FC9:2016:A1B2C3", "usb:1FC9:2016:D4E5F6"} {
		if !strings.Contains(err.Error(), s) {
			t.Errorf("%q does not name %s", err, s)
		}
	}
	if len(bus.written) != 0 {
		t.Errorf("written to %q", bus.written)
	}
}

// What a page is told when the write to the printer fails, by the Win32
// error it failed with.
func TestAFailedWriteSaysWhy(t *testing.T) {
	for _, tc := range []struct {
		name string
		os   error
		want error // nil: none of the known causes, so print_failed
	}{
		{"sharing violation", syscall.Errno(32), ErrBusy},
		{"busy", syscall.Errno(170), ErrBusy},
		{"timed out and cancelled", syscall.Errno(258), ErrNotResponding},
		{"aborted", syscall.Errno(995), ErrNotResponding},
		{"unplugged since it was found", syscall.Errno(1167), ErrNotFound},
		{"gone since it was found", syscall.Errno(2), ErrNotFound},
		{"access denied", syscall.Errno(5), nil},
		{"general failure", syscall.Errno(31), nil},
	} {
		bus := &fakeBus{connected: []busDevice{{xp1, xpID}}, fails: map[string]error{xp1: tc.os}}
		err := USB{ID: USBID{VID: 0x1FC9, PID: 0x2016}, bus: bus}.Print(context.Background(), []byte("x"))
		if err == nil {
			t.Errorf("%s: no error", tc.name)
			continue
		}
		for _, known := range []error{ErrNotFound, ErrAmbiguous, ErrBusy, ErrNotResponding} {
			if errors.Is(err, known) != (known == tc.want) {
				t.Errorf("%s: %v; errors.Is(%v) = %v, want %v", tc.name, err, known, !(known == tc.want), known == tc.want)
			}
		}
	}
}

// The printer gets about 10 s to take a job, so that a jammed printer is
// reported as not responding instead of holding the page's request forever.
func TestTheWriteHasAbout10Seconds(t *testing.T) {
	bus := &fakeBus{connected: []busDevice{{xp1, xpID}}}
	start := time.Now()
	if err := (USB{ID: USBID{VID: 0x1FC9, PID: 0x2016}, bus: bus}).Print(context.Background(), []byte("x")); err != nil {
		t.Fatal(err)
	}
	if d := bus.deadline.Sub(start); d < 9*time.Second || d > 11*time.Second {
		t.Errorf("the write's deadline is %v away, want about 10 s", d)
	}
}

// The connected USB printers, known models or not, as install lists them
// when it finds no known one.
func TestConnectedUSBPrintersAreListedWithTheir1284Names(t *testing.T) {
	bus := &fakeBus{connected: []busDevice{
		{xp1, xpID},
		{other, "MANUFACTURER:Acme;MODEL:Label 2;"},
		{xpNoSerial, ""},
		{`\\?\root#something#0000#{28d78fad-5a12-11d1-ae5b-0000f803a8c2}`, ""},
	}}
	printers, err := namedUSB(bus)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, p := range printers {
		got = append(got, p.String())
	}
	want := []string{`1FC9:2016 "Xprinter XP-365B"`, `0483:5720 "Acme Label 2"`, `1FC9:2016 ""`}
	if !slices.Equal(got, want) {
		t.Errorf("listed %q, want %q", got, want)
	}
	if printers[1].Serial != "" {
		t.Errorf("composite device serial %q, want none", printers[1].Serial)
	}
}
