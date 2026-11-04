package printer

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strconv"
	"strings"
)

// sysfsDevices is where Linux lists USB devices, readable without root.
const sysfsDevices = "/sys/bus/usb/devices/"

// usbPrinterClass is the USB interface class of printers.
const usbPrinterClass = 7

// sysfsUSB is the connected USB printers Linux lists: the devices with a
// printer interface, such as 1-7 for its interface 1-7:1.0. A name is the
// printer's IEEE 1284 ID when the kernel's usblp driver read it, since the USB
// strings of some printers are not their model.
func sysfsUSB(c Computer) ([]USBPrinter, error) {
	entries, err := c.ReadDir(sysfsDevices[:len(sysfsDevices)-1])
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	read := func(path string) string {
		b, err := c.ReadFile(sysfsDevices + path)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(b))
	}
	var printers []USBPrinter
	var seen []string
	for _, entry := range entries {
		device, _, isInterface := strings.Cut(entry, ":")
		if !isInterface || slices.Contains(seen, device) {
			continue
		}
		if class, err := strconv.ParseUint(read(entry+"/bInterfaceClass"), 16, 8); err != nil || class != usbPrinterClass {
			continue
		}
		seen = append(seen, device)
		name := name1284(read(entry + "/ieee1284_id"))
		if name == "" {
			name = strings.TrimSpace(read(device+"/manufacturer") + " " + read(device+"/product"))
		}
		if p, ok := newUSBPrinter(read(device+"/idVendor"), read(device+"/idProduct"), 16, read(device+"/serial"), name); ok {
			printers = append(printers, p)
		}
	}
	return printers, nil
}

// ioregUSB is the connected USB printers macOS lists: the devices in the I/O
// Registry with a printer interface under them. A device under a hub is listed
// again under the hub, so one seen at the same location is left out.
func ioregUSB(ctx context.Context, c Computer) ([]USBPrinter, error) {
	out, err := c.Run(ctx, nil, "ioreg", "-r", "-c", "IOUSBHostDevice", "-l", "-w", "0")
	if err != nil {
		return nil, fmt.Errorf("list USB devices: %w", err)
	}
	var printers []USBPrinter
	var seen []string
	var device map[string]string
	isPrinter := false
	flush := func() {
		location := device["locationID"]
		if !isPrinter || (location != "" && slices.Contains(seen, location)) {
			return
		}
		seen = append(seen, location)
		// The registry's numbers are decimal unless they say otherwise.
		name := strings.TrimSpace(device["USB Vendor Name"] + " " + device["USB Product Name"])
		if p, ok := newUSBPrinter(device["idVendor"], device["idProduct"], 0, device["USB Serial Number"], name); ok {
			printers = append(printers, p)
		}
	}
	for line := range strings.Lines(string(out)) {
		if strings.Contains(line, "<class IOUSBHostDevice,") {
			flush()
			device, isPrinter = map[string]string{}, false
			continue
		}
		_, property, ok := strings.Cut(line, `"`)
		if !ok || device == nil {
			continue
		}
		key, value, ok := strings.Cut(property, `" = `)
		if !ok {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), `"`)
		// Each interface has a class of its own, and a device any number of them.
		if key == "bInterfaceClass" {
			class, err := strconv.ParseUint(value, 0, 8)
			isPrinter = isPrinter || (err == nil && class == usbPrinterClass)
		}
		if _, have := device[key]; !have {
			device[key] = value
		}
	}
	flush()
	return printers, nil
}

// newUSBPrinter is the printer with these IDs as the OS writes them in base;
// false when they are no IDs.
func newUSBPrinter(vid, pid string, base int, serial, name string) (USBPrinter, bool) {
	v, err1 := strconv.ParseUint(vid, base, 16)
	p, err2 := strconv.ParseUint(pid, base, 16)
	if err1 != nil || err2 != nil {
		return USBPrinter{}, false
	}
	return USBPrinter{USBID: USBID{VID: uint16(v), PID: uint16(p), Serial: strings.ToUpper(serial)}, Name: name}, true
}
