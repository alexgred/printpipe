// Package layout is where printpipe keeps its files for the current user, on
// each OS. There is no PID file: the running agent is found by its port.
package layout

import "strings"

// Dirs are the user's folders the paths are made from.
type Dirs struct {
	GOOS string
	Home string
	// Config is os.UserConfigDir.
	Config string
	// LocalAppData is %LOCALAPPDATA%, on Windows only.
	LocalAppData string
}

// Paths are printpipe's own files.
type Paths struct {
	Binary, Config, Log string
}

// For is the files in dirs. It joins by the target OS's rule rather than the
// running one's, so that every row can be tested on any OS.
func For(d Dirs) Paths {
	switch d.GOOS {
	case "windows":
		backslashed := func(parts ...string) string { return strings.Join(parts, `\`) }
		return Paths{
			Binary: backslashed(d.LocalAppData, "Programs", "printpipe", "printpipe.exe"),
			Config: backslashed(d.Config, "printpipe", "config.json"),
			Log:    backslashed(d.LocalAppData, "printpipe", "agent.log"),
		}
	case "darwin":
		return Paths{
			Binary: join(d.Home, ".local", "bin", "printpipe"),
			Config: join(d.Config, "printpipe", "config.json"),
			Log:    join(d.Home, "Library", "Logs", "printpipe.log"),
		}
	default:
		return Paths{
			Binary: join(d.Home, ".local", "bin", "printpipe"),
			Config: join(d.Config, "printpipe", "config.json"),
			Log:    join(d.Home, ".local", "state", "printpipe", "agent.log"),
		}
	}
}

func join(parts ...string) string { return strings.Join(parts, "/") }
