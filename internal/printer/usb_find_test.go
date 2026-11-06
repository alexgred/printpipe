//go:build !windows

package printer

import (
	"context"
	"fmt"
	"reflect"
	"testing"
)

// As on Fedora with an XP-365B: the USB strings are not its model, the IEEE
// 1284 ID is.
func TestSysfsFindsUSBPrintersOnly(t *testing.T) {
	const d = "/sys/bus/usb/devices/"
	c := fakeComputer{goos: "linux", files: map[string]string{
		d + "1-5/idVendor":            "046d\n",
		d + "1-5/idProduct":           "c52b\n",
		d + "1-5:1.0/bInterfaceClass": "03\n",
		d + "usb1/idVendor":           "1d6b\n",
		d + "usb1/idProduct":          "0002\n",
		d + "1-7/idVendor":            "1fc9\n",
		d + "1-7/idProduct":           "2016\n",
		d + "1-7/serial":              "0020426981aa\n",
		d + "1-7/manufacturer":        "Printer\n",
		d + "1-7/product":             "Printer-80\n",
		d + "1-7:1.0/bInterfaceClass": "07\n",
		d + "1-7:1.0/ieee1284_id":     "MFG:Xprinter;CMD:TSPL2;MDL:XP-365B;CLS:PRINTER;\n",
		d + "1-7:1.1/bInterfaceClass": "07\n",
		d + "1-9/idVendor":            "04b8\n",
		d + "1-9/idProduct":           "0202\n",
		d + "1-9/manufacturer":        "EPSON\n",
		d + "1-9/product":             "TM-T20\n",
		d + "1-9:1.0/bInterfaceClass": "07\n",
	}}
	got, err := ListUSB(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	want := []USBPrinter{
		{USBID: USBID{VID: 0x1FC9, PID: 0x2016, Serial: "0020426981AA"}, Name: "Xprinter XP-365B"},
		{USBID: USBID{VID: 0x04B8, PID: 0x0202}, Name: "EPSON TM-T20"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestSysfsWithoutUSBFindsNothing(t *testing.T) {
	if got, err := ListUSB(context.Background(), fakeComputer{goos: "linux"}); err != nil || len(got) != 0 {
		t.Errorf("got %+v, %v; want nothing", got, err)
	}
}

const ioregOutput = `+-o USB Receiver@14200000  <class IOUSBHostDevice, id 0x100000a11, registered, matched, active, busy 0 (5 ms), retain 20>
  | {
  |   "idProduct" = 50475
  |   "idVendor" = 1133
  |   "locationID" = 337641472
  |   "USB Product Name" = "USB Receiver"
  | }
  |
  +-o IOUSBHostInterface@0  <class IOUSBHostInterface, id 0x100000a12, registered, matched, active, busy 0 (1 ms), retain 8>
    | {
    |   "bInterfaceClass" = 3
    | }
+-o Printer-80@14100000  <class IOUSBHostDevice, id 0x100000abc, registered, matched, active, busy 0 (5 ms), retain 20>
  | {
  |   "idProduct" = 8214
  |   "idVendor" = 8137
  |   "locationID" = 336592896
  |   "USB Product Name" = "Printer-80"
  |   "USB Vendor Name" = "Printer"
  |   "USB Serial Number" = "0020426981aa"
  | }
  |
  +-o IOUSBHostInterface@0  <class IOUSBHostInterface, id 0x100000abd, registered, matched, active, busy 0 (1 ms), retain 8>
    | {
    |   "bInterfaceClass" = 3
    | }
  +-o IOUSBHostInterface@1  <class IOUSBHostInterface, id 0x100000abe, registered, matched, active, busy 0 (1 ms), retain 8>
    | {
    |   "bInterfaceClass" = 7
    | }
`

func TestIORegistryFindsUSBPrintersOnly(t *testing.T) {
	// A device under a hub is listed once for itself and again under the hub.
	c := fakeComputer{goos: "darwin", commands: map[string]string{"ioreg -r -c IOUSBHostDevice -l -w 0": ioregOutput + ioregOutput}}
	got, err := ListUSB(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	want := []USBPrinter{{USBID: USBID{VID: 0x1FC9, PID: 0x2016, Serial: "0020426981AA"}, Name: "Printer Printer-80"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// Two printers without a location are still two printers.
func TestIORegistryKeepsPrintersWithoutALocationApart(t *testing.T) {
	const printer = `+-o P@1  <class IOUSBHostDevice, id 0x1>
  |   "idProduct" = %d
  |   "idVendor" = 8137
  |   "bInterfaceClass" = 7
`
	c := fakeComputer{goos: "darwin", commands: map[string]string{"ioreg -r -c IOUSBHostDevice -l -w 0": fmt.Sprintf(printer, 1) + fmt.Sprintf(printer, 2)}}
	got, err := ListUSB(context.Background(), c)
	if err != nil || len(got) != 2 {
		t.Errorf("got %+v, %v; want two printers", got, err)
	}
}
