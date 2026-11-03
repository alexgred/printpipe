package printer

import (
	"context"
	"fmt"
	"syscall"
	"unsafe"
)

// Queue sends jobs to a Windows printer with the RAW datatype through the
// spooler (OpenPrinter, StartDocPrinter, WritePrinter), so that the driver
// passes the bytes to the printer untouched.
type Queue struct {
	Name string
}

var (
	winspool         = syscall.NewLazyDLL("winspool.drv")
	openPrinter      = winspool.NewProc("OpenPrinterW")
	closePrinter     = winspool.NewProc("ClosePrinter")
	startDocPrinter  = winspool.NewProc("StartDocPrinterW")
	endDocPrinter    = winspool.NewProc("EndDocPrinter")
	startPagePrinter = winspool.NewProc("StartPagePrinter")
	endPagePrinter   = winspool.NewProc("EndPagePrinter")
	writePrinter     = winspool.NewProc("WritePrinter")
)

// errorInvalidPrinterName is what OpenPrinter fails with when there is no
// such printer.
const errorInvalidPrinterName syscall.Errno = 1801

// docInfo1 is DOC_INFO_1W.
type docInfo1 struct {
	docName    *uint16
	outputFile *uint16
	datatype   *uint16
}

func (q Queue) Print(_ context.Context, job []byte) error {
	if len(job) == 0 {
		return nil
	}
	fail := func(step string, err error) error {
		return fmt.Errorf("printer %s: %s: %w", q.Name, step, err)
	}
	name, err := syscall.UTF16PtrFromString(q.Name)
	if err != nil {
		return fail("name", err)
	}
	var h syscall.Handle
	if r, _, err := openPrinter.Call(uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(&h)), 0); r == 0 {
		if err == errorInvalidPrinterName {
			return fmt.Errorf("printer %s: %w", q.Name, ErrNotFound)
		}
		return fail("OpenPrinter", err)
	}
	defer closePrinter.Call(uintptr(h))

	doc := docInfo1{
		docName:  syscall.StringToUTF16Ptr("printpipe job"),
		datatype: syscall.StringToUTF16Ptr("RAW"),
	}
	if r, _, err := startDocPrinter.Call(uintptr(h), 1, uintptr(unsafe.Pointer(&doc))); r == 0 {
		return fail("StartDocPrinter", err)
	}
	defer endDocPrinter.Call(uintptr(h))
	if r, _, err := startPagePrinter.Call(uintptr(h)); r == 0 {
		return fail("StartPagePrinter", err)
	}
	defer endPagePrinter.Call(uintptr(h))

	for len(job) > 0 {
		var written uint32
		r, _, err := writePrinter.Call(uintptr(h), uintptr(unsafe.Pointer(&job[0])), uintptr(len(job)), uintptr(unsafe.Pointer(&written)))
		if r == 0 {
			return fail("WritePrinter", err)
		}
		if written == 0 {
			return fail("WritePrinter", fmt.Errorf("nothing was written"))
		}
		job = job[written:]
	}
	return nil
}
