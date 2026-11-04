package layout

import "testing"

func TestPathsPerOS(t *testing.T) {
	for _, tc := range []struct {
		dirs Dirs
		want Paths
	}{
		{
			Dirs{GOOS: "linux", Home: "/home/me", Config: "/home/me/.config"},
			Paths{
				Binary: "/home/me/.local/bin/printpipe",
				Config: "/home/me/.config/printpipe/config.json",
				Log:    "/home/me/.local/state/printpipe/agent.log",

				ConfigDir: "/home/me/.config/printpipe",
				LogDir:    "/home/me/.local/state/printpipe",

				BinaryDir: "/home/me/.local/bin",
				NewBinary: "/home/me/.local/bin/.printpipe.new",
				OldBinary: "/home/me/.local/bin/.printpipe.old",
			},
		},
		{
			Dirs{GOOS: "darwin", Home: "/Users/me", Config: "/Users/me/Library/Application Support"},
			Paths{
				Binary: "/Users/me/.local/bin/printpipe",
				Config: "/Users/me/Library/Application Support/printpipe/config.json",
				Log:    "/Users/me/Library/Logs/printpipe.log",

				ConfigDir: "/Users/me/Library/Application Support/printpipe",

				BinaryDir: "/Users/me/.local/bin",
				NewBinary: "/Users/me/.local/bin/.printpipe.new",
				OldBinary: "/Users/me/.local/bin/.printpipe.old",
			},
		},
		{
			Dirs{GOOS: "windows", Home: `C:\Users\me`, Config: `C:\Users\me\AppData\Roaming`, LocalAppData: `C:\Users\me\AppData\Local`},
			Paths{
				Binary: `C:\Users\me\AppData\Local\Programs\printpipe\printpipe.exe`,
				Config: `C:\Users\me\AppData\Roaming\printpipe\config.json`,
				Log:    `C:\Users\me\AppData\Local\printpipe\agent.log`,

				ConfigDir: `C:\Users\me\AppData\Roaming\printpipe`,
				LogDir:    `C:\Users\me\AppData\Local\printpipe`,

				BinaryDir: `C:\Users\me\AppData\Local\Programs\printpipe`,
				NewBinary: `C:\Users\me\AppData\Local\Programs\printpipe\.printpipe.new`,
				OldBinary: `C:\Users\me\AppData\Local\Programs\printpipe\.printpipe.old`,
			},
		},
	} {
		t.Run(tc.dirs.GOOS, func(t *testing.T) {
			if got := For(tc.dirs); got != tc.want {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}
