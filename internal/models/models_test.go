package models

import (
	"bytes"
	"testing"

	"github.com/alexgred/printpipe/internal/testlabel"
)

func TestTheXP365BIsKnownByItsUSBIDsAndIts1284ID(t *testing.T) {
	byUSB, ok := ByUSB(0x1FC9, 0x2016)
	if !ok || byUSB.Name != "XP-365B" {
		t.Fatalf("ByUSB(1FC9, 2016) = %+v, %v; want the XP-365B", byUSB, ok)
	}
	if byUSB.TestLabelSize != (testlabel.Size{Width: 58, Height: 40}) {
		t.Errorf("test label size %v, want 58x40", byUSB.TestLabelSize)
	}
	if !bytes.HasPrefix(byUSB.PPD, []byte("*PPD-Adobe:")) {
		t.Errorf("PPD starts with %q, want a PPD", byUSB.PPD[:min(len(byUSB.PPD), 20)])
	}
	for _, id := range []string{
		"MFG:Xprinter;MDL:XP-365B;",
		"MANUFACTURER:Xprinter;CMD:TSPL;MODEL:XP-365B;CLS:PRINTER;",
		"mfg:XPRINTER; mdl:xp-365b",
	} {
		if m, ok := By1284ID(id); !ok || m.Name != "XP-365B" {
			t.Errorf("By1284ID(%q) = %+v, %v; want the XP-365B", id, m, ok)
		}
	}
}

func TestADeviceSettingNamesItsModel(t *testing.T) {
	for _, device := range []string{"usb:1FC9:2016", "usb:1fc9:2016:A1B2C3"} {
		if m, ok := ForDevice(device); !ok || m.Name != "XP-365B" {
			t.Errorf("ForDevice(%q) = %+v, %v; want the XP-365B", device, m, ok)
		}
	}
}

// printpipe never prints to a device it does not know.
func TestUnknownModelsAreNeverMatched(t *testing.T) {
	if m, ok := ByUSB(0x1FC9, 0x2017); ok {
		t.Errorf("another PID matched %+v", m)
	}
	if m, ok := ByUSB(0x0483, 0x2016); ok {
		t.Errorf("another VID matched %+v", m)
	}
	for _, id := range []string{"MFG:Xprinter;MDL:XP-370B;", "MFG:Zebra;MDL:XP-365B;", "MDL:XP-365B;", "MFG:Xprinter;", ""} {
		if m, ok := By1284ID(id); ok {
			t.Errorf("By1284ID(%q) matched %+v", id, m)
		}
	}
	for _, device := range []string{"usb:1FC9:2017", "/dev/usb/lp0", "usb:1FC9", "usb:xyz:2016", ""} {
		if m, ok := ForDevice(device); ok {
			t.Errorf("ForDevice(%q) matched %+v", device, m)
		}
	}
}
