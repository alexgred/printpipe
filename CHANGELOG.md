# Changelog

All notable changes to printpipe. Versions follow [semver](https://semver.org/).

## Unreleased

### Changed

- The XP-365B's test label is made for a 40×30 mm roll, not 58×40: `test-print` and `install` without `--size` print `SIZE 40 mm, 30 mm`, and the CUPS queue `install` creates has a 40×30 mm page in its PPD. `--size 58,40` prints the old size.

### Fixed

- On Windows, `install` with several connected printers of a model printpipe does not know matching `usb:VID:PID` no longer panics: it exits with 1, `error: 2 printers match usb:VID:PID` and a `printpipe config --device` command per printer.
- A printer that its device string cannot tell apart from another that matches, with no serial or with a serial another has too, gets no command; the hint to leave one plugged in is printed once instead. Two printers with the same serial no longer get the same command twice.
- The label limit of `POST /print` counts CR, LF and CRLF line ends alike: a `PRINT` after a lone CR is counted, so a job over 10 labels is `bad_job` and a CR-only job of 1 to 10 labels prints.
- `install` checks a saved or given device file (Linux) or Windows queue before it starts the agent. One that is gone fails with exit 1, says what to do and keeps the origin in `config.json`; a check that fails exits with 1 and the reason. Neither is replaced by a printer `install` finds.
- `test-print` on `printer_ambiguous` lists the connected printers that match the configured device and prints a `printpipe config --device` command per printer with its real serial, falling back to the old hint when they cannot be listed or fewer than two match.

## v0.2.0

The first release.

- One line installs printpipe: `install.sh` and `install.ps1` download the build for this computer to a temp folder, check it against `SHA256SUMS`, run its `install` and delete it. `make release` builds windows/amd64, darwin/amd64, darwin/arm64 and linux/amd64 locally and publishes them with `gh`.
- One binary for the command and the background agent: `printpipe version`, `printpipe help`; the agent runs as the hidden `printpipe serve`.
- Configuration in `config.json` in the user's configuration folder: `port` (default 8631), `origins`, `queue`, `device`.
- `GET /health` answers `{"name": "printpipe", "version": …, "pid": …}` with CORS for the requesting origin.
- `POST /print` answers every error as JSON `{"code", "message"}` (`printer_not_found`, `printer_ambiguous`, `printer_busy`, `printer_not_responding`, `print_failed`, `origin_not_allowed`, `bad_job`), and every answer, the 403 included, carries CORS for the requesting origin. No `Access-Control-Allow-Private-Network` header.
- A built-in list of known printer models, the Xprinter XP-365B (USB `1FC9:2016`) first; a model not in it is never matched.
- `printpipe test-print [--size W,H]` prints the built-in test label through the running agent: TSPL in CP1251 for the model's roll (XP-365B: 58×40 mm) with one Cyrillic line in font 3.
- Windows prints straight to the USB printer (the usbprint device interface): no admin rights, no driver, no queue. `device: "usb:VID:PID[:serial]"` is found again on every job; several matches are `printer_ambiguous`, none `printer_not_found`, another program holding it `printer_busy`, a write not done in about 10 s `printer_not_responding`. An existing Windows queue is used only when given as `queue`.
- macOS and Linux print through a CUPS queue with `lp -o raw`: a missing queue is `printer_not_found`, any other failure, CUPS not being installed included, `print_failed`. A `device` file on Linux answers the same codes, a write not done in about 10 s `printer_not_responding`; without permission `test-print` says `no permission to write to …` and how to join the `lp` group.
- Finding printers without root, for `install` to use: usable CUPS queues through `lpstat -e` and `lpoptions -p`, USB printers through sysfs (Linux) and `ioreg` (macOS).
- `printpipe start`, `stop`, `restart` and `startup [on|off]` on Linux, through the systemd user unit `printpipe.service`; without a user manager a detached child, SIGTERM after checking its program file, and an entry in `~/.config/autostart/`. `start` on a running agent and `stop` on a stopped one succeed and say so; another program on the port is never touched.
- Per-user file layout for Windows, macOS and Linux. The agent writes its own log of about 1 MB plus one previous file, with why it refused to run and each job that did not print.
- `printpipe start`, `stop`, `restart` and `startup [on|off]` on Windows, with no admin rights: a process with no console that outlives the terminal, stopped through the event `Local\printpipe-stop` and then by force after checking its program file, and the value `printpipe` in the user's HKCU `Run` key. A manifest in the binary keeps Windows 11 24H2 and later from opening a console at logon.
- `printpipe config` shows the configuration; `--queue`, `--device`, `--origin` (adds, duplicates ignored), `--remove-origin` and `--port` check the change, write it, say what changed and restart a running agent. `--port` warns that a client's agent URL must use the same port, and a taken port leaves a running agent as it is.
- On macOS and Linux, when no usable queue exists and a known model is connected, printpipe creates its queue with one elevation: `sudo lpinfo --include-schemes usb -v`, then `sudo lpadmin -p <name> -E -v <uri> -P <embedded PPD> -o printer-is-shared=false`. printpipe never runs as root, never overwrites a queue of the user's, and remembers the queue it created as `created_queue` in `config.json`.
- `printpipe start`, `stop`, `restart` and `startup [on|off]` on macOS, not yet run on a real Mac: launchd owns the agent as the service `io.github.alexgred.printpipe`, through a plist in printpipe's Application Support folder, and `startup` copies it to `~/Library/LaunchAgents` or removes it, never with `launchctl disable`.
- `printpipe install --origin <url>` takes a computer from nothing to a running agent and a printed test label, and can be run again at any time, with `--queue`, `--device`, `--port`, `--size` and `--no-test-print`. It places itself by renames (`.printpipe.new`, `.printpipe.old`), which works over a running agent; takes a browser's `Zone.Identifier` or quarantine mark off the installed file; adds its folder to the user's `Path` on Windows, and elsewhere prints the line to add. With no printer it writes the configuration and starts nothing, a foreign program on the port is left alone, and a test label not sent leaves the agent running: each exits with 1.
- With several known printers connected, `install` prints a ready command per printer; on macOS and Linux `install --device usb:VID:PID:<serial>` chooses the one to make a queue for.
- The systemd unit, the launchd plist, the XDG autostart entry and the HKCU `Run` value always run the installed binary, not the copy that wrote them, which may disappear. From a copy with nothing installed, `startup on` and `start` under a manager say to run `install` first.
- `printpipe status` shows offline whether the agent runs and under which manager (`launchd`, `systemd` or no service manager) with its pid, the installed and running versions with a suggestion to `restart` when they differ, the port, whether it starts at login, the configured queue or device and whether the OS has it, and the paths of the binary, config and log. It exits with 1 when the agent is not running, and does not check the printer's paper or state.
- `printpipe upgrade` installs the latest release: it reads the tag from the redirect of `/releases/latest`, compares by semver before downloading (the same or a newer version installed succeeds and changes nothing; a build that is no semver counts as older), checks the build against `SHA256SUMS`, runs it, swaps it in by renames, restarts only a running agent and waits for it to answer as the new version. A failure before the swap leaves everything as it was; a new version that does not come up is rolled back, `rolled back to X, log: <path>`, exit 1. It is refused from a copy that is not installed, and changes only the binary.
- `printpipe remove` stops the agent and takes off the computer its binary, config, log and what starts it (the systemd unit, the XDG autostart entry, both launchd plists, the HKCU `Run` value and the folder in the user's `Path`), and keeps the printer queue. `--queue` on macOS and Linux also deletes the queue printpipe created, and never another; on Windows it says no queue was created and succeeds. Run again, it succeeds. Another program on the port is left alone, and an agent run from another file stops it before anything changes.
