package printer

import "errors"

// The failures a page can act on. A printer wraps one of them (with %w) when
// it knows why a job did not go out; any other error is just "printing
// failed".
var (
	// ErrNotFound is the configured device or queue not being there.
	ErrNotFound = errors.New("printer not found")
	// ErrAmbiguous is several connected devices matching the configured one.
	ErrAmbiguous = errors.New("several printers match")
	// ErrBusy is another program holding the device.
	ErrBusy = errors.New("printer busy")
	// ErrNotResponding is the job not being written in time.
	ErrNotResponding = errors.New("printer not responding")
)
