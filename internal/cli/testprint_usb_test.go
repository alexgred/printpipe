//go:build !windows

package cli

import (
	"fmt"
	"testing"

	"github.com/alexgred/printpipe/internal/printer"
)

// The agent says only that several printers match; the CLI looks which, for a
// command that chooses each. ListUSB is the OS's own on the test's OS, the sysfs
// of a Linux computer here.
func TestTestPrintWithSeveralMatchingPrintersGivesACommandPerDevice(t *testing.T) {
	sys, p := newPrinting(t)
	plugUSB(sys, "1-7", "1fc9", "2016", "0020426981aa")
	plugUSB(sys, "1-8", "1fc9", "2016", "SECOND")
	p.err = fmt.Errorf("printer usb:1FC9:2016: %w", printer.ErrAmbiguous)

	got := run(t, sys, "test-print")

	wantFailure(t, got, "error: the test label was not sent: several printers match the configured device\n"+
		"  printpipe config --device usb:1FC9:2016:0020426981AA\n"+
		"  printpipe config --device usb:1FC9:2016:SECOND\n"+
		"  Then: printpipe test-print\n"+
		"  The agent is running.\n")
}

// What the agent saw may be gone by the time the CLI looks, or the CLI cannot look.
func TestTestPrintWithSeveralMatchingPrintersItCannotListSaysHowToChoose(t *testing.T) {
	const want = "error: the test label was not sent: several printers match the configured device\n" +
		"  Choose one with printpipe config --device usb:VID:PID:<serial>, then: printpipe test-print\n" +
		"  The agent is running.\n"
	for _, tc := range []struct {
		name  string
		setup func(*fakeSystem)
	}{
		{"one left", func(sys *fakeSystem) {
			plugUSB(sys, "1-7", "1fc9", "2016", "ONE")
			plugUSB(sys, "1-8", "04b8", "0202", "OTHER")
		}},
		// macOS lists them with ioreg, which is not there.
		{"the list fails", func(sys *fakeSystem) { sys.goos = "darwin" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sys, p := newPrinting(t)
			tc.setup(sys)
			p.err = fmt.Errorf("printer usb:1FC9:2016: %w", printer.ErrAmbiguous)
			wantFailure(t, run(t, sys, "test-print"), want)
		})
	}
}
