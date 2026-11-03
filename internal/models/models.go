// Package models is the built-in list of label printer models printpipe
// knows. It finds and sets up only these on its own: support for a new model
// is one entry in known, and a model not in it is never matched, so that
// printpipe never prints to the wrong device.
package models

import (
	_ "embed"
	"strconv"
	"strings"

	"github.com/alexgred/printpipe/internal/testlabel"
)

// Model is a label printer model printpipe knows.
type Model struct {
	// Name is how the CLI names it, such as XP-365B.
	Name string
	// VID and PID are its USB vendor and product IDs.
	VID, PID uint16
	// MFG and MDL are its IEEE 1284 device ID's manufacturer and model.
	MFG, MDL string
	// TestLabelSize is the size of the roll the test label is made for.
	TestLabelSize testlabel.Size
	// PPD is the minimal CUPS PPD a queue for it is created with.
	PPD []byte
}

//go:embed xp-365b.ppd
var xp365bPPD []byte

var known = []Model{
	{
		Name: "XP-365B",
		VID:  0x1FC9, PID: 0x2016,
		MFG: "Xprinter", MDL: "XP-365B",
		TestLabelSize: testlabel.Size{Width: 58, Height: 40},
		PPD:           xp365bPPD,
	},
}

// Default is the model printpipe assumes when the printer's own is not
// known, such as for a queue: the first one in the list.
func Default() Model { return known[0] }

// ByUSB is the model with these USB IDs.
func ByUSB(vid, pid uint16) (Model, bool) {
	for _, m := range known {
		if m.VID == vid && m.PID == pid {
			return m, true
		}
	}
	return Model{}, false
}

// By1284ID is the model an IEEE 1284 device ID names, such as
// "MFG:Xprinter;MDL:XP-365B;" (the long keys MANUFACTURER and MODEL too).
// Both the manufacturer and the model must match, ignoring case.
func By1284ID(id string) (Model, bool) {
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
	for _, m := range known {
		if strings.EqualFold(m.MFG, mfg) && strings.EqualFold(m.MDL, mdl) {
			return m, true
		}
	}
	return Model{}, false
}

// ForDevice is the model a config device names: usb:VID:PID[:serial] with
// the IDs in hex. A device file path names no model.
func ForDevice(device string) (Model, bool) {
	rest, ok := strings.CutPrefix(device, "usb:")
	if !ok {
		return Model{}, false
	}
	parts := strings.SplitN(rest, ":", 3)
	if len(parts) < 2 {
		return Model{}, false
	}
	vid, err1 := strconv.ParseUint(parts[0], 16, 16)
	pid, err2 := strconv.ParseUint(parts[1], 16, 16)
	if err1 != nil || err2 != nil {
		return Model{}, false
	}
	return ByUSB(uint16(vid), uint16(pid))
}
