package cli

import "errors"

// Running in the background on Windows comes with its own ticket.

func (OS) Spawn(string, ...string) error  { return errors.ErrUnsupported }
func (OS) Terminate(int) error            { return errors.ErrUnsupported }
func (OS) ProcessExe(int) (string, error) { return "", errors.ErrUnsupported }
