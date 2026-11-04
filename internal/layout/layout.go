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
	// ConfigDir and LogDir are the folders only printpipe's files are in, which
	// remove takes away too. The log on macOS has no such folder.
	ConfigDir, LogDir string
	// BinaryDir is the folder of Binary. NewBinary and OldBinary are beside it in
	// a swap: the program coming in, and the one that was there.
	BinaryDir, NewBinary, OldBinary string
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

			ConfigDir: backslashed(d.Config, "printpipe"),
			LogDir:    backslashed(d.LocalAppData, "printpipe"),

			BinaryDir: backslashed(d.LocalAppData, "Programs", "printpipe"),
			NewBinary: backslashed(d.LocalAppData, "Programs", "printpipe", ".printpipe.new"),
			OldBinary: backslashed(d.LocalAppData, "Programs", "printpipe", ".printpipe.old"),
		}
	case "darwin":
		return Paths{
			Binary: join(d.Home, ".local", "bin", "printpipe"),
			Config: join(d.Config, "printpipe", "config.json"),
			Log:    join(d.Home, "Library", "Logs", "printpipe.log"),

			ConfigDir: join(d.Config, "printpipe"),

			BinaryDir: join(d.Home, ".local", "bin"),
			NewBinary: join(d.Home, ".local", "bin", ".printpipe.new"),
			OldBinary: join(d.Home, ".local", "bin", ".printpipe.old"),
		}
	default:
		return Paths{
			Binary: join(d.Home, ".local", "bin", "printpipe"),
			Config: join(d.Config, "printpipe", "config.json"),
			Log:    join(d.Home, ".local", "state", "printpipe", "agent.log"),

			ConfigDir: join(d.Config, "printpipe"),
			LogDir:    join(d.Home, ".local", "state", "printpipe"),

			BinaryDir: join(d.Home, ".local", "bin"),
			NewBinary: join(d.Home, ".local", "bin", ".printpipe.new"),
			OldBinary: join(d.Home, ".local", "bin", ".printpipe.old"),
		}
	}
}

func join(parts ...string) string { return strings.Join(parts, "/") }
