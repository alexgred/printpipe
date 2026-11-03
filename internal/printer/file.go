// Package printer hands print jobs to the printer: through the OS print queue
// in RAW mode, straight to a device file, or, in dry run, to a file or the log.
package printer

import (
	"context"
	"os"
)

// File is the dry run: each job replaces the file's contents, so the file
// holds exactly the bytes of the last job.
type File struct {
	Path string
}

func (f File) Print(_ context.Context, job []byte) error {
	return os.WriteFile(f.Path, job, 0o644)
}
