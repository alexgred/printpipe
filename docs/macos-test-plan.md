# macOS test plan

The first run of printpipe on a real Mac, Apple Silicon or Intel. The steps themselves are in `TESTING.md`; this file puts the macOS ones in order, riskiest first, and says what to keep. Record results in the tables of `TESTING.md`, as for Windows and Fedora.

Setup: an ordinary user logged in to the desktop, an XP-365B on USB with a 40×30 roll, nothing of printpipe installed, nothing on port 8631. Note the macOS version, the chip and the printpipe build.

## 1. Keep the raw output first

printpipe parses `ioreg`, `lpinfo` and `launchctl print` from what is known of their format, not from a real Mac. Before installing, save:

```sh
sw_vers > sw_vers.txt; uname -m >> sw_vers.txt
ioreg -r -c IOUSBHostDevice -l -w 0 > ioreg.txt
sudo lpinfo --include-schemes usb -v > lpinfo.txt
LC_ALL=C.UTF-8 lpstat -r > lpstat.txt; lpstat -e >> lpstat.txt 2>&1; echo "exit $?" >> lpstat.txt
```

Once the agent runs (step 2), also:

```sh
launchctl print gui/$UID/io.github.alexgred.printpipe > launchctl.txt
```

If parsing fails, these files become the test fixtures for the fix.

- [ ] `ioreg.txt`, `lpinfo.txt`, `lpstat.txt`, `launchctl.txt` saved
- [ ] `lpstat -r` says `scheduler is running`
- [ ] What `lpstat -e` does with no queue at all, if that is the case: noted

## 2. The one-line install

`TESTING.md` → "The one-line install", steps 5–7 done on the Mac. One command covers download, finding the printer, creating the queue, launchd and the test label.

```sh
curl -fsSL https://github.com/alexgred/printpipe/releases/latest/download/install.sh | sh -s -- --origin https://app.example
```

- [ ] One line per step, the label comes out, Cyrillic in Cyrillic, `echo $?` is `0`
- [ ] `sudo` asks for the password at the terminal, though the script came through a pipe
- [ ] `ls ${TMPDIR:-/tmp}` has no `tmp.*` folder left by it
- [ ] `printpipe status` shows the agent `running under launchd` from `~/.local/bin`, or says how to add it to `PATH`
- [ ] `xattr ~/.local/bin/printpipe` has no `com.apple.quarantine`; `codesign -v ~/.local/bin/printpipe` passes
- [ ] Networking off: `could not reach GitHub`, exit 1

## 3. CUPS

`TESTING.md` → "macOS: through CUPS" and "Linux: creating a CUPS queue".

- [ ] `printer.ListUSB` finds the printer; note its name (`Xprinter XP-365B` or `Printer Printer-80`)
- [ ] `lpinfo` names it `usb://Xprinter/XP-365B`
- [ ] `lpadmin -P` takes the minimal PPD; `lpoptions -p XP-365B` has the `device-uri` and `printer-is-shared=false`
- [ ] `config.json` has `"queue"` and `"created_queue"`, no `.ppd` left beside it
- [ ] `"queue": "no-such-queue"`: `test-print` says `the printer was not found`
- [ ] `printpipe install` again: no password, no new queue, `Agent restarted`

## 4. launchd

`TESTING.md` → "macOS: running in the background", steps 1–11. The fragile ones:

- [ ] 1: `start` works; `plutil -lint` accepts the plist; `launchctl print` has `pid = N` matching `/health` and `program = <binary>`. A second `start` says `already running`
- [ ] 2: the agent survives closing the terminal
- [ ] 3: `restart` gives a new `pid` on `/health` right away
- [ ] 4: `stop`, a second `stop` says `not running`, `restart` starts it
- [ ] 5: a hand-changed plist, then `restart`: no `Bootstrap failed: 5`
- [ ] 6: `startup on`, log out and in: the agent runs; `stop` works; `startup off`, log out and in: it does not
- [ ] 7: `kill -SEGV` → launchd restarts it within ~10 s; a broken config → `the agent did not answer within 10 s`
- [ ] 8: `serve` in a terminal, `stop` from another ends it
- [ ] 9: port 8631 busy: `port 8631 is used by another program`, exit 1
- [ ] 10: over `ssh` with no desktop login: `start` reports what `launchctl` says, nothing stays loaded
- [ ] 11: a copy in `/tmp` (`/private/tmp`): `serve`, then `stop` from the same copy; note what it says

## 5. The rest of the commands

Each section of `TESTING.md`, run on the Mac:

- [ ] `install`, steps 1–11 (step 3: `xattr` on the installed binary)
- [ ] `status`, steps 1–5, 8, 9
- [ ] `config`, steps 1–5
- [ ] `upgrade`, steps 1–4, 6–9 (step 7: launchd's `KeepAlive` did not hold the file; step 9: `codesign -v` and no quarantine)
- [ ] `remove`, steps 1–6 (step 2: both plists gone, `launchctl print` finds no service, no agent after logging in again)

## Minimum

If time is short: sections 1–3 and steps 1–6 of section 4. That is the path a real user takes.
