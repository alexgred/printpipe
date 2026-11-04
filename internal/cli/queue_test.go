//go:build !windows

package cli

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/alexgred/printpipe/internal/config"
	"github.com/alexgred/printpipe/internal/models"
)

const xpURI = "usb://Xprinter/XP-365B?serial=0020426981AA"

// cupsComputer makes sys a computer with CUPS and an XP-365B plugged in: sudo
// lpadmin adds to the queues lpstat and lpoptions list, which start as lpoptions'
// line by name.
func cupsComputer(sys *fakeSystem, queues map[string]string, lpinfo string) {
	const d = "/sys/bus/usb/devices/"
	sys.files[d+"1-7/idVendor"] = []byte("1fc9\n")
	sys.files[d+"1-7/idProduct"] = []byte("2016\n")
	sys.files[d+"1-7/serial"] = []byte("0020426981aa\n")
	sys.files[d+"1-7:1.0/bInterfaceClass"] = []byte("07\n")
	sys.files[d+"1-7:1.0/ieee1284_id"] = []byte("MFG:Xprinter;MDL:XP-365B;\n")
	cups(sys, queues, lpinfo)
}

// cups is CUPS with these queues on a computer with whatever is plugged in.
func cups(sys *fakeSystem, queues map[string]string, lpinfo string) {
	sys.programs["lpstat"] = func(args []string) ([]byte, error) {
		if args[0] == "-r" {
			return []byte("scheduler is running\n"), nil
		}
		var names []string
		for name := range queues {
			names = append(names, name)
		}
		slices.Sort(names)
		return []byte(strings.Join(names, "\n")), nil
	}
	sys.programs["lpoptions"] = func(args []string) ([]byte, error) { return []byte(queues[args[1]]), nil }
	sys.programs["sudo"] = func(args []string) ([]byte, error) {
		switch args[0] {
		case "lpinfo":
			return []byte(lpinfo), nil
		case "lpadmin":
			if args[1] == "-x" {
				delete(queues, args[2])
				return nil, nil
			}
			queues[args[2]] = "device-uri=" + xpURI + " printer-is-accepting-jobs=true"
			return nil, nil
		}
		return nil, fmt.Errorf("unexpected sudo %q", args)
	}
}

// sudoCommands is each command run as root.
func sudoCommands(sys *fakeSystem) []string {
	var commands []string
	for _, ran := range sys.ran {
		if command, ok := strings.CutPrefix(ran.command, "sudo "); ok {
			commands = append(commands, command)
		}
	}
	return commands
}

// savedConfig is config.json as a later command reads it.
func savedConfig(t *testing.T, sys *fakeSystem) config.Config {
	t.Helper()
	files, err := filesOf(sys)
	if err != nil {
		t.Fatal(err)
	}
	b, ok := sys.files[files.Config]
	if !ok {
		return config.Default()
	}
	cfg, err := config.Parse(b)
	if err != nil {
		t.Fatalf("config.json: %v", err)
	}
	return cfg
}

func TestInstallCreatesAQueueForAKnownModelWithSystemCommandsOnlyAsRoot(t *testing.T) {
	for _, goos := range []string{"linux", "darwin"} {
		t.Run(goos, func(t *testing.T) {
			sys := newFakeSystem()
			if goos == "darwin" {
				sys, _ = onMac()
			}
			cupsComputer(sys, map[string]string{}, "direct usb://HP/LaserJet?serial=9\ndirect "+xpURI+"\n")
			var ppdAtCreation []byte
			var ppdPath string
			lpadmin := sys.programs["sudo"]
			sys.programs["sudo"] = func(args []string) ([]byte, error) {
				if args[0] == "lpadmin" {
					ppdPath = args[slices.Index(args, "-P")+1]
					ppdAtCreation = sys.files[ppdPath]
				}
				return lpadmin(args)
			}

			got := runInstall(t, sys)

			if got.code != 0 {
				t.Fatalf("got %+v, want exit 0", got)
			}
			want := []string{
				"lpinfo --include-schemes usb -v",
				"lpadmin -p XP-365B -E -v " + xpURI + " -P " + ppdPath + " -o printer-is-shared=false",
			}
			if commands := sudoCommands(sys); !slices.Equal(commands, want) {
				t.Errorf("ran as root %q, want %q", commands, want)
			}
			if string(ppdAtCreation) != string(models.Default().PPD) {
				t.Errorf("lpadmin read a PPD of %d bytes, want the embedded one", len(ppdAtCreation))
			}
			if _, left := sys.files[ppdPath]; left {
				t.Errorf("the PPD is left at %s", ppdPath)
			}
			cfg := savedConfig(t, sys)
			if cfg.Queue != "XP-365B" || cfg.CreatedQueue != "XP-365B" {
				t.Errorf("config has queue %q and created queue %q, want both XP-365B", cfg.Queue, cfg.CreatedQueue)
			}
			if !strings.Contains(got.stdout, "password") || !strings.Contains(got.stdout, "Printer: queue XP-365B") {
				t.Errorf("stdout %q, want why the password is asked and the printer", got.stdout)
			}
		})
	}
}

func TestInstallAsksForNoPasswordWhenAQueueIsThereOrChosen(t *testing.T) {
	usable := "device-uri=" + xpURI + " printer-is-accepting-jobs=true"
	for _, tc := range []struct {
		name      string
		config    string
		queues    map[string]string
		wantQueue string
		created   string
	}{
		{"a usable queue of the user's", `{}`, map[string]string{"mine": usable}, "mine", ""},
		{"the configured queue, whatever it prints to", `{"queue": "office"}`, map[string]string{"office": "device-uri=ipp://office.local"}, "office", ""},
		{"a configured device", `{"device": "/dev/usb/lp0"}`, map[string]string{}, "", ""},
		{"the queue it created before", `{"queue": "XP-365B", "created_queue": "XP-365B"}`, map[string]string{"XP-365B": usable}, "XP-365B", "XP-365B"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sys := newFakeSystem()
			cupsComputer(sys, tc.queues, "direct "+xpURI+"\n")
			files, _ := filesOf(sys)
			sys.files[files.Config] = []byte(tc.config)

			got := runInstall(t, sys)

			if got.code != 0 {
				t.Fatalf("got %+v, want exit 0", got)
			}
			if commands := sudoCommands(sys); len(commands) != 0 {
				t.Errorf("ran as root %q, want nothing", commands)
			}
			if cfg := savedConfig(t, sys); cfg.Queue != tc.wantQueue || cfg.CreatedQueue != tc.created {
				t.Errorf("config has queue %q and created queue %q, want %q and %q", cfg.Queue, cfg.CreatedQueue, tc.wantQueue, tc.created)
			}
		})
	}
}

// A queue that only calls itself a label printer may print to anything, so it is
// taken only when no known model could have a queue instead.
func TestInstallPrefersAConnectedKnownModelToAQueueThatOnlyLooksLikeALabelPrinter(t *testing.T) {
	label := func() map[string]string {
		return map[string]string{"shop": "device-uri=usb://ACME/P1 printer-is-accepting-jobs=true printer-make-and-model='ACME Label Printer'"}
	}

	t.Run("the known model is plugged in", func(t *testing.T) {
		sys := newFakeSystem()
		cupsComputer(sys, label(), "direct usb://ACME/P1\ndirect "+xpURI+"\n")
		if got := runInstall(t, sys); got.code != 0 {
			t.Fatalf("got %+v, want exit 0", got)
		}
		if cfg := savedConfig(t, sys); cfg.Queue != "XP-365B" || cfg.CreatedQueue != "XP-365B" {
			t.Errorf("config has queue %q and created queue %q, want both XP-365B", cfg.Queue, cfg.CreatedQueue)
		}
	})

	t.Run("no known model is plugged in", func(t *testing.T) {
		sys := newFakeSystem()
		cups(sys, label(), "")
		if got := runInstall(t, sys); got.code != 0 {
			t.Fatalf("got %+v, want exit 0", got)
		}
		if cfg := savedConfig(t, sys); cfg.Queue != "shop" || cfg.CreatedQueue != "" {
			t.Errorf("config has queue %q and created queue %q, want shop and none", cfg.Queue, cfg.CreatedQueue)
		}
		if commands := sudoCommands(sys); len(commands) != 0 {
			t.Errorf("ran as root %q, want nothing", commands)
		}
	})
}

func TestInstallRunTwiceCreatesOneQueue(t *testing.T) {
	sys := newFakeSystem()
	cupsComputer(sys, map[string]string{}, "direct "+xpURI+"\n")
	if got := runInstall(t, sys); got.code != 0 {
		t.Fatalf("first run: %+v", got)
	}
	created := len(sudoCommands(sys))

	got := runInstall(t, sys)

	if got.code != 0 || len(sudoCommands(sys)) != created {
		t.Errorf("second run: %+v, ran as root %q; want no more than the first run's", got, sudoCommands(sys))
	}
	if cfg := savedConfig(t, sys); cfg.CreatedQueue != "XP-365B" {
		t.Errorf("created queue %q, want it still remembered", cfg.CreatedQueue)
	}
}

// lpadmin -p changes a queue that has the name, and remove --queue deletes what
// printpipe remembers: neither may reach a queue the user made.
func TestInstallLeavesAQueueOfTheUsersWithTheSameNameAlone(t *testing.T) {
	stopped := "device-uri=" + xpURI + " printer-is-accepting-jobs=false"
	sys := newFakeSystem()
	cupsComputer(sys, map[string]string{"xp-365b": stopped, "XP-365B-2": "device-uri=ipp://office.local printer-is-accepting-jobs=true"}, "direct "+xpURI+"\n")

	got := runInstall(t, sys)

	if got.code != 0 {
		t.Fatalf("got %+v, want exit 0", got)
	}
	if cfg := savedConfig(t, sys); cfg.Queue != "XP-365B-3" || cfg.CreatedQueue != "XP-365B-3" {
		t.Errorf("config has queue %q and created queue %q, want both XP-365B-3", cfg.Queue, cfg.CreatedQueue)
	}
}

// A queue printpipe made that stopped taking jobs is its own to repair, and a
// new one would leave it behind with nothing remembering it.
func TestInstallRepairsAQueueItCreatedThatStoppedTakingJobs(t *testing.T) {
	sys := newFakeSystem()
	cupsComputer(sys, map[string]string{"XP-365B": "device-uri=" + xpURI + " printer-is-accepting-jobs=false"}, "direct "+xpURI+"\n")
	files, _ := filesOf(sys)
	sys.files[files.Config] = []byte(`{"created_queue": "XP-365B"}`)

	if got := runInstall(t, sys); got.code != 0 {
		t.Fatalf("got %+v, want exit 0", got)
	}
	if cfg := savedConfig(t, sys); cfg.Queue != "XP-365B" || cfg.CreatedQueue != "XP-365B" {
		t.Errorf("config has queue %q and created queue %q, want both XP-365B", cfg.Queue, cfg.CreatedQueue)
	}
}

// The base name being free again is no reason to leave the queue it remembers
// behind, under a suffix.
func TestInstallRepairsTheQueueItCreatedEvenWhenTheBaseNameIsFree(t *testing.T) {
	sys := newFakeSystem()
	cupsComputer(sys, map[string]string{"XP-365B-2": "device-uri=" + xpURI + " printer-is-accepting-jobs=false"}, "direct "+xpURI+"\n")
	files, _ := filesOf(sys)
	sys.files[files.Config] = []byte(`{"created_queue": "XP-365B-2"}`)

	if got := runInstall(t, sys); got.code != 0 {
		t.Fatalf("got %+v, want exit 0", got)
	}
	if cfg := savedConfig(t, sys); cfg.Queue != "XP-365B-2" || cfg.CreatedQueue != "XP-365B-2" {
		t.Errorf("config has queue %q and created queue %q, want both XP-365B-2", cfg.Queue, cfg.CreatedQueue)
	}
}

// Two printers are seen without root, so no password is asked for nothing.
func TestInstallWithTwoOfTheModelPluggedInAsksForNoPassword(t *testing.T) {
	const d = "/sys/bus/usb/devices/"
	sys := newFakeSystem()
	cupsComputer(sys, map[string]string{}, "direct "+xpURI+"\n")
	sys.files[d+"1-8/idVendor"] = []byte("1fc9\n")
	sys.files[d+"1-8/idProduct"] = []byte("2016\n")
	sys.files[d+"1-8:1.0/bInterfaceClass"] = []byte("07\n")

	got := runInstall(t, sys)

	want := "error: 2 XP-365B printers are connected\n" +
		"  printpipe install --device usb:1FC9:2016:0020426981AA\n" +
		"  A printer with no serial number cannot be chosen among its model: leave one plugged in and run the same command again.\n"
	if got.code != 1 || got.stderr != want {
		t.Errorf("got %+v, want exit 1 and\n%s", got, want)
	}
	if commands := sudoCommands(sys); len(commands) != 0 {
		t.Errorf("ran as root %q, want nothing", commands)
	}
}

// Failures, with sudo's and the programs' words as OS.Run reports them.
func TestInstallFailingToCreateAQueueSaysWhatToDo(t *testing.T) {
	const existing = "  An existing queue: printpipe config --queue <name>\n"
	refused := "error: could not create the printer queue: administrator rights were refused\n" +
		"  Run the same command again and enter your computer password when asked.\n" + existing
	noCUPS := "error: CUPS is not installed (lpadmin not found)\n  Install CUPS and run the same command again.\n"
	for _, tc := range []struct {
		name    string
		failing string // the program run as root that fails
		err     string
		want    string
	}{
		{"wrong password", "lpinfo", "sudo: exit status 1: Sorry, try again.\nsudo: 3 incorrect password attempts", refused},
		{"no terminal", "lpinfo", "sudo: exit status 1: sudo: a terminal is required to read the password", refused},
		{"not in sudoers", "lpinfo", "sudo: exit status 1: me is not in the sudoers file.", refused},
		{"no reason given", "lpinfo", "sudo: exit status 1", refused},
		{"refused at the second command", "lpadmin", "sudo: exit status 1: sudo: a password is required", refused},
		{"no lpinfo", "lpinfo", "sudo: exit status 1: sudo: lpinfo: command not found", noCUPS},
		{"no lpadmin", "lpadmin", "sudo: exit status 1: sudo: lpadmin: command not found", noCUPS},
		{"lpadmin refuses", "lpadmin", "sudo: exit status 1: lpadmin: Unable to copy PPD file",
			"error: could not create the printer queue: sudo: exit status 1: lpadmin: Unable to copy PPD file\n" + existing},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sys := newFakeSystem()
			cupsComputer(sys, map[string]string{}, "direct "+xpURI+"\n")
			sudo := sys.programs["sudo"]
			sys.programs["sudo"] = func(args []string) ([]byte, error) {
				if args[0] == tc.failing {
					return nil, errors.New(tc.err)
				}
				return sudo(args)
			}

			got := runInstall(t, sys)

			if got.code != 1 || got.stderr != tc.want {
				t.Errorf("got %+v, want exit 1 and\n%s", got, tc.want)
			}
			if cfg := savedConfig(t, sys); cfg.Queue != "" || cfg.CreatedQueue != "" {
				t.Errorf("config has queue %q and created queue %q, want neither", cfg.Queue, cfg.CreatedQueue)
			}
			for name := range sys.files {
				if strings.HasSuffix(name, ".ppd") {
					t.Errorf("the PPD is left at %s", name)
				}
			}
		})
	}
}

func TestInstallWithoutCUPSSaysToInstallIt(t *testing.T) {
	got := runInstall(t, newFakeSystem())
	want := "error: CUPS is not installed (lpadmin not found)\n  Install CUPS and run the same command again.\n"
	if got.code != 1 || got.stderr != want {
		t.Errorf("got %+v, want exit 1 and\n%s", got, want)
	}
}

func TestInstallWithTheSchedulerDownCreatesNoQueue(t *testing.T) {
	sys := newFakeSystem()
	cupsComputer(sys, map[string]string{}, "direct "+xpURI+"\n")
	sys.programs["lpstat"] = func([]string) ([]byte, error) { return []byte("scheduler is not running\n"), nil }

	got := runInstall(t, sys)

	if got.code != 1 || !strings.Contains(got.stderr, "scheduler is not running") {
		t.Errorf("got %+v, want exit 1 and the scheduler named", got)
	}
	if commands := sudoCommands(sys); len(commands) != 0 {
		t.Errorf("ran as root %q, want nothing", commands)
	}
}

func TestInstallWithNoQueueAndNoKnownModelAsksForNoPassword(t *testing.T) {
	const d = "/sys/bus/usb/devices/"
	for _, tc := range []struct {
		name  string
		files map[string]string
	}{
		{"nothing plugged in", nil},
		{"an unknown printer", map[string]string{
			d + "1-9/idVendor": "04b8", d + "1-9/idProduct": "0202", d + "1-9:1.0/bInterfaceClass": "07",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sys := newFakeSystem()
			for name, content := range tc.files {
				sys.files[name] = []byte(content)
			}
			cups(sys, map[string]string{}, "")

			got := runInstall(t, sys)

			want := "error: no label printer queue and no known printer found\n" +
				"  Plug the XP-365B in, switch it on and run the same command again.\n" +
				"  An existing queue: printpipe config --queue <name>\n"
			if got.code != 1 || got.stderr != want {
				t.Errorf("got %+v, want exit 1 and\n%s", got, want)
			}
			if commands := sudoCommands(sys); len(commands) != 0 {
				t.Errorf("ran as root %q, want nothing", commands)
			}
		})
	}
}

// printpipe never guesses which printer a label should go to.
func TestInstallCreatesNoQueueWhenCUPSDoesNotPointAtOnePrinter(t *testing.T) {
	const existing = "  An existing queue: printpipe config --queue <name>\n"
	for _, tc := range []struct {
		name, lpinfo, want string
	}{
		{"CUPS does not list it", "direct usb://HP/LaserJet?serial=9\n",
			"error: CUPS does not list the connected XP-365B\n  Switch the printer off and on and run the same command again.\n" + existing},
		{"two of the model", "direct " + xpURI + "\ndirect usb://Xprinter/XP-365B?serial=2\n",
			"error: 2 XP-365B printers are connected\n  printpipe install --device usb:1FC9:2016:0020426981AA\n  printpipe install --device usb:1FC9:2016:2\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sys := newFakeSystem()
			cupsComputer(sys, map[string]string{}, tc.lpinfo)

			got := runInstall(t, sys)

			if got.code != 1 || got.stderr != tc.want {
				t.Errorf("got %+v, want exit 1 and\n%s", got, tc.want)
			}
			if commands := sudoCommands(sys); len(commands) != 1 {
				t.Errorf("ran as root %q, want lpinfo only", commands)
			}
		})
	}
}
