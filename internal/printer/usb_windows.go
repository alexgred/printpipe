package printer

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"syscall"
	"time"
	"unsafe"
)

// osBus is Windows' usbprint device interface: found through SetupAPI and
// written with CreateFile/WriteFile, all without admin rights, a driver or
// a queue.
type osBus struct{}

// guidDevInterfaceUSBPrint is GUID_DEVINTERFACE_USBPRINT,
// {28d78fad-5a12-11d1-ae5b-0000f803a8c2}.
var guidDevInterfaceUSBPrint = syscall.GUID{
	Data1: 0x28d78fad, Data2: 0x5a12, Data3: 0x11d1,
	Data4: [8]byte{0xae, 0x5b, 0x00, 0x00, 0xf8, 0x03, 0xa8, 0xc2},
}

var (
	setupapi                        = syscall.NewLazyDLL("setupapi.dll")
	setupDiGetClassDevs             = setupapi.NewProc("SetupDiGetClassDevsW")
	setupDiEnumDeviceInterfaces     = setupapi.NewProc("SetupDiEnumDeviceInterfaces")
	setupDiGetDeviceInterfaceDetail = setupapi.NewProc("SetupDiGetDeviceInterfaceDetailW")
	setupDiDestroyDeviceInfoList    = setupapi.NewProc("SetupDiDestroyDeviceInfoList")
	kernel32                        = syscall.NewLazyDLL("kernel32.dll")
	createEvent                     = kernel32.NewProc("CreateEventW")
	getOverlappedResult             = kernel32.NewProc("GetOverlappedResult")
)

const (
	digcfPresent         = 0x02
	digcfDeviceInterface = 0x10

	errorInsufficientBuffer syscall.Errno = 122
	errorNoMoreItems        syscall.Errno = 259

	// ioctlUSBPrintGet1284ID is IOCTL_USBPRINT_GET_1284_ID:
	// CTL_CODE(FILE_DEVICE_UNKNOWN, 13, METHOD_BUFFERED, FILE_ANY_ACCESS).
	ioctlUSBPrintGet1284ID = 0x220034
)

// spDeviceInterfaceData is SP_DEVICE_INTERFACE_DATA.
type spDeviceInterfaceData struct {
	size     uint32
	class    syscall.GUID
	flags    uint32
	reserved uintptr
}

func (osBus) paths() ([]string, error) {
	set, _, err := setupDiGetClassDevs.Call(uintptr(unsafe.Pointer(&guidDevInterfaceUSBPrint)), 0, 0, digcfPresent|digcfDeviceInterface)
	if syscall.Handle(set) == syscall.InvalidHandle {
		return nil, err
	}
	defer setupDiDestroyDeviceInfoList.Call(set)

	var paths []string
	for i := uintptr(0); ; i++ {
		data := spDeviceInterfaceData{size: uint32(unsafe.Sizeof(spDeviceInterfaceData{}))}
		if r, _, err := setupDiEnumDeviceInterfaces.Call(set, 0, uintptr(unsafe.Pointer(&guidDevInterfaceUSBPrint)), i, uintptr(unsafe.Pointer(&data))); r == 0 {
			if err == errorNoMoreItems {
				return paths, nil
			}
			return nil, err
		}
		var need uint32
		r, _, err := setupDiGetDeviceInterfaceDetail.Call(set, uintptr(unsafe.Pointer(&data)), 0, 0, uintptr(unsafe.Pointer(&need)), 0)
		if r == 0 && err != errorInsufficientBuffer {
			return nil, err
		}
		// SP_DEVICE_INTERFACE_DETAIL_DATA_W: a DWORD cbSize, then the path.
		// cbSize is the struct's own size, 8 on 64-bit and 6 on 32-bit
		// Windows; the buffer is []uint64 for its alignment.
		buf := make([]uint64, (need+7)/8)
		if unsafe.Sizeof(uintptr(0)) == 4 {
			*(*uint32)(unsafe.Pointer(&buf[0])) = 6
		} else {
			*(*uint32)(unsafe.Pointer(&buf[0])) = 8
		}
		if r, _, err := setupDiGetDeviceInterfaceDetail.Call(set, uintptr(unsafe.Pointer(&data)), uintptr(unsafe.Pointer(&buf[0])), uintptr(need), 0, 0); r == 0 {
			return nil, err
		}
		path := unsafe.Slice((*uint16)(unsafe.Add(unsafe.Pointer(&buf[0]), 4)), (int(need)-4)/2)
		paths = append(paths, syscall.UTF16ToString(path))
	}
}

func (osBus) deviceID(path string) string {
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return ""
	}
	// No access asked: the IOCTL needs none, and the printer's job is not
	// in the way.
	h, err := syscall.CreateFile(name, 0, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE, nil, syscall.OPEN_EXISTING, 0, 0)
	if err != nil {
		return ""
	}
	defer syscall.CloseHandle(h)
	// The answer is a big-endian length, these two bytes included, and the
	// ID.
	var buf [1024]byte
	var n uint32
	if err := syscall.DeviceIoControl(h, ioctlUSBPrintGet1284ID, nil, 0, &buf[0], uint32(len(buf)), &n, nil); err != nil || n < 2 {
		return ""
	}
	size := min(int(buf[0])<<8|int(buf[1]), int(n))
	if size < 2 {
		return ""
	}
	return string(buf[2:size])
}

// usbWrite is one overlapped write in progress. The kernel fills it in after
// WriteFile returns, so it is pinned on the heap until the write is over.
type usbWrite struct {
	ov syscall.Overlapped
	n  uint32
	// pin keeps the usbWrite in place; job is kept for a stuck write.
	pin runtime.Pinner
	job []byte
}

// stuck keeps the writes whose cancelling never finished: the kernel may
// still fill them in, so they are never let go.
var stuck []*usbWrite

// cancelWait is how long a cancelled write gets to be over.
const cancelWait = 5 * time.Second

func (osBus) write(ctx context.Context, path string, job []byte) error {
	if len(job) == 0 {
		return nil
	}
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	// No sharing: a program holding the printer makes this fail with a
	// sharing violation (busy), and no one writes between our bytes.
	h, err := syscall.CreateFile(name, syscall.GENERIC_WRITE, 0, nil, syscall.OPEN_EXISTING, syscall.FILE_FLAG_OVERLAPPED, 0)
	if err != nil {
		return err
	}
	ev, _, err := createEvent.Call(0, 1, 0, 0)
	if ev == 0 {
		syscall.CloseHandle(h)
		return err
	}
	w := &usbWrite{}
	w.pin.Pin(w)
	for len(job) > 0 {
		w.ov = syscall.Overlapped{HEvent: syscall.Handle(ev)}
		w.n = 0
		err = syscall.WriteFile(h, job, &w.n, &w.ov)
		if err == nil || err == syscall.ERROR_IO_PENDING {
			err = w.wait(ctx, h)
		}
		if err == errStuck {
			// The handle, the event, the job and w stay as they are: the
			// kernel may still use them. The next job finds the printer busy.
			w.job = job
			stuck = append(stuck, w)
			return errorWaitTimeout
		}
		if err != nil {
			break
		}
		if w.n == 0 {
			err = errors.New("WriteFile: nothing was written")
			break
		}
		job = job[w.n:]
	}
	syscall.CloseHandle(syscall.Handle(ev))
	syscall.CloseHandle(h)
	w.pin.Unpin()
	return err
}

// errStuck is a cancelled write that did not end.
var errStuck = errors.New("the cancelled write did not end")

// wait waits for the write until ctx's deadline or until ctx is done, then
// cancels it: the printer not taking the job by the deadline is
// errorWaitTimeout.
func (w *usbWrite) wait(ctx context.Context, h syscall.Handle) error {
	deadline, hasDeadline := ctx.Deadline()
	for {
		// Short waits, so that a page giving up is noticed too.
		slice := 100 * time.Millisecond
		if hasDeadline {
			slice = min(slice, max(time.Until(deadline), 0))
		}
		ev, err := syscall.WaitForSingleObject(w.ov.HEvent, uint32(slice/time.Millisecond))
		switch {
		case ev == syscall.WAIT_OBJECT_0:
			return w.result(h)
		case ev != syscall.WAIT_TIMEOUT:
			if err == nil {
				err = fmt.Errorf("WaitForSingleObject: %#x", ev)
			}
			return w.cancel(h, err)
		case ctx.Err() == context.DeadlineExceeded || hasDeadline && !time.Now().Before(deadline):
			return w.cancel(h, errorWaitTimeout)
		case ctx.Err() != nil:
			return w.cancel(h, ctx.Err())
		}
	}
}

// cancel ends the write with why, unless it finished just before.
func (w *usbWrite) cancel(h syscall.Handle, why error) error {
	_ = syscall.CancelIoEx(h, &w.ov)
	if ev, _ := syscall.WaitForSingleObject(w.ov.HEvent, uint32(cancelWait/time.Millisecond)); ev != syscall.WAIT_OBJECT_0 {
		return errStuck
	}
	err := w.result(h)
	if err == nil {
		return nil
	}
	if err == errorOperationAborted {
		return why
	}
	return err
}

// result is the finished write's outcome, its count in w.n.
func (w *usbWrite) result(h syscall.Handle) error {
	if r, _, err := getOverlappedResult.Call(uintptr(h), uintptr(unsafe.Pointer(&w.ov)), uintptr(unsafe.Pointer(&w.n)), 0); r == 0 {
		return err
	}
	return nil
}
