package printer

import "context"

// Computer is what printing through CUPS or a device file, and finding
// printers, needs of the computer, so that tests drive it against a fake.
type Computer interface {
	// Run runs a program with stdin, speaking English whatever the user's
	// language, and returns its stdout. A program that fails returns an error
	// naming what it wrote to stderr; one that is not installed returns an
	// error wrapping exec.ErrNotFound.
	Run(ctx context.Context, stdin []byte, name string, args ...string) ([]byte, error)
	// WriteDevice sends the whole job to a device file by ctx's deadline,
	// failing with the OS's own error, or ctx's when the deadline passes.
	WriteDevice(ctx context.Context, path string, job []byte) error
	// ReadDir is the names in a folder.
	ReadDir(name string) ([]string, error)
	ReadFile(name string) ([]byte, error)
}
