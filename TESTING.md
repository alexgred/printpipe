# Testing

`make test` covers everything up to the OS: the HTTP server, the CLI over a fake system, the test label, the `usb:` device setting with its error mapping, CUPS printing and finding printers over faked program output. The real OS adapters are checked by hand on real machines, with the steps below, before each release.

## Windows: straight to the USB device

On a Windows 11 PC as an ordinary user (no admin), an XP-365B on USB with a 58×40 roll, and no driver or queue installed for it:

1. Build: `make build`, copy `dist/printpipe-windows-amd64.exe` to the PC as `printpipe.exe`.
2. Write `%APPDATA%\printpipe\config.json`:
   ```json
   {"origins": ["https://app.example"], "device": "usb:1FC9:2016"}
   ```
3. In one PowerShell window: `.\printpipe.exe serve`. The log line names `printer: USB printer usb:1FC9:2016`.
4. In another: `.\printpipe.exe test-print`. Expect `A test label should have come out of the printer`, and a label with `printpipe test print`, the version, the date and time, and the Cyrillic line printed in Cyrillic (not as `?` or other characters).
5. Move the printer to another USB port and run `test-print` again: it still prints.
6. Unplug the printer: `test-print` says `the printer was not found`.
7. Plug it back in, open the cover or take out the roll and send a few labels until the printer stops taking data: within about 10 s `test-print` says `the printer did not respond`, and the agent keeps answering.
8. With a second XP-365B plugged in: `test-print` says `several printers match the configured device`; the `/print` answer's `message` names `usb:1FC9:2016:<serial>` for each. With `"device": "usb:1FC9:2016:<serial>"` the chosen one prints.
9. With a Windows queue on the same printer (if one exists): `"queue": "<its name>"` instead of `device` prints the test label through the spooler.

Not verified yet: whether a vendor driver holds the device open, and whether a printer held by another program fails with a sharing violation (`printer_busy`) or with access denied (today `print_failed`), and whether other XP-365B units share PID `0x2016`.

| date | Windows | printer | build | result |
|---|---|---|---|---|
| 2026-11-04 | 11 | XP-365B | 7fcc04c | Steps 3, 4 (Cyrillic prints), 5 and 6 pass. Step 7: with the cover open `test-print` reported an error; its exact text was not recorded. Step 8 not run: no second printer. Step 9 not reported. |

## Linux: through CUPS

On Fedora as an ordinary user not in the `lp` group, an XP-365B on USB with a 58×40 roll and a CUPS queue for it (below, `XP-365B`). With `"port"` and `"origins"` in `~/.config/printpipe/config.json`, run `printpipe serve` in one terminal and, in another, after each change of config (restart `serve`):

1. `"queue": "XP-365B"`: `printpipe test-print` says `A test label should have come out of the printer`, and the label comes out with the Cyrillic line in Cyrillic.
2. `"queue": "no-such-queue"`: `test-print` says `the printer was not found`.
3. `"device": "/dev/usb/lp0"`: `test-print` says `no permission to write to /dev/usb/lp0` and `sudo usermod -aG lp $USER, then log out and back in.`
4. After joining the `lp` group: the same config prints the test label.
5. `"device": "/dev/usb/lp9"`: `test-print` says `the printer was not found`.
6. Still as in step 4, open the cover or take out the roll and send labels until the printer stops taking data: within about 10 s `test-print` says `the printer did not respond`, and the agent keeps answering.
7. Finding printers (nothing calls it until `install`): `printer.ListUSB` names `1FC9:2016 "Xprinter XP-365B"` and `models.UsableQueues` lists the queue, both without root.

8. `"queue": "XP-365B"` with `CUPS_SERVER=127.0.0.1:1` set for `serve` (as if the scheduler were down): `test-print` says `printing failed` and `the CUPS scheduler is not running`, not that the printer was not found.

Not verified yet: what `lpstat -e` does on a computer with no queue at all; whether a retry after step 6 fails as `printer_busy` while the timed-out write still holds the device, and whether that job prints late.

| date | Linux | printer | build | result |
|---|---|---|---|---|
| 2026-11-04 | Fedora 44 | XP-365B | the commit adding this table | Steps 2, 3, 5, 7 and 8 pass. Step 1 passes: the label came out with Cyrillic in Cyrillic, too close to the left edge, so the text moved 1 mm right. With the cover open, through the queue: `test-print` says the label should have come out and CUPS completes the job in about 8 s with no error (`XP-365B-16`). The label came out once the cover was closed, with the new margin. Steps 4 and 6 not run: the user is not in `lp`. |
| 2026-11-04 | Fedora 44 | XP-365B | the commit adding this row | Device file, with write access given by an ACL instead of the `lp` group. Before the fix `test-print` succeeded but no label came out, cover open (6 labels) or closed (2), while `cat` to the same file printed: `os.OpenFile` had made the device non-blocking. With a blocking write step 4 prints. Step 6 not finished: with the cover open the printer took 3 labels at once, with `test-print` saying they should have come out, and all 3 came out once the cover was closed. 3 labels are too few to make it stop taking data, so the timeout was not seen. |

## Linux: running in the background

On Fedora as an ordinary user, logged in to a desktop session, with a usable `~/.config/printpipe/config.json` and `printpipe` in `~/.local/bin` (nothing else answering on the port):

1. `printpipe start` says `Agent started, version X`. `systemctl --user status printpipe.service` shows it active, and `~/.config/systemd/user/printpipe.service` runs that binary. A second `start` says `already running, version X`.
2. Close the terminal and open another: `printpipe test-print` still prints.
3. `printpipe restart` says `Agent restarted, version X`, and `/health` answers a new `pid`.
4. `printpipe stop` says `Agent stopped`; `systemctl --user status printpipe.service` shows it inactive. A second `stop` says `not running`. `restart` now starts it.
5. `printpipe startup` says `off`. `startup on` says `on`, leaves the agent as it was, and `systemctl --user is-enabled printpipe.service` says `enabled`. Log out and back in: the agent runs. `startup off` leaves it running, and after another login it does not run.
6. Kill the agent with `kill -SEGV <pid>`: the unit starts it again within a few seconds. Then break the config (remove `queue` and `device`), `stop`, `start`: after 10 s `the agent did not answer within 10 s` and the log path, the log says `no printer`, and the unit does not keep restarting. Put the config back.
7. With the agent stopped, `printpipe serve` in a terminal, then `printpipe stop` in another: the agent exits, though the unit did not run it. The same with `restart`: the unit runs it afterwards.
8. With the agent stopped, `python3 -m http.server 8631 --bind 127.0.0.1`: `start`, `stop` and `restart` each say `port 8631 is used by another program`, exit 1, and the server keeps running.
9. Without a user manager (`ssh` in as a user with no session, or `env -u XDG_RUNTIME_DIR -u DBUS_SESSION_BUS_ADDRESS printpipe …`): `start` gives a detached child that survives closing the terminal, `stop` ends it, and `startup on` writes `~/.config/autostart/printpipe.desktop`, which starts the agent at the next desktop login.
10. Write more than 1 MB to the log (`make dev`-style `printpipe serve --dry-run -` with jobs from a page, or fill `agent.log` with `truncate -s 1048570`, then `restart`): `agent.log.1` holds the old log and `agent.log` starts anew.

Checked without a real manager: on 2026-11-04 steps 1, 3, 4, 5 (`startup` only) and 8 of the detached child, with `systemctl` off the `PATH`, a separate `HOME` and another port; `systemd-analyze verify` accepts the unit. Not run yet: everything through a real `systemctl --user`.

## `config` with a running agent

On each OS as an ordinary user, with a usable config, the agent running and nothing else on port 8631 or 9100:

1. `printpipe config` shows the port, origins, queue and device.
2. `printpipe config --origin https://b.example` says `Origins: added https://b.example` and `Agent restarted, version X`, and `/health` answers a new `pid`.
3. `printpipe config --port 9100` says what changed, warns about the client's agent URL and says `Agent restarted`; `/health` now answers on 9100 and nothing answers on 8631. `printpipe config --port 8631` moves it back.
4. With `python3 -m http.server 9100 --bind 127.0.0.1` running: `printpipe config --port 9100` says `port 9100 is used by another program`, exit 1; the agent still answers on 8631 and `config.json` is unchanged.
5. `printpipe stop`, then `printpipe config --port 9100`: it says the agent is not running and does not start it. `printpipe start` then uses 9100.

Not run yet on any OS.

## Windows: running in the background

On a Windows 11 PC as an ordinary user (no admin), with a usable `%APPDATA%\printpipe\config.json` and `printpipe.exe` in a folder of its own (nothing else answering on the port). Note the build of Windows (`winver`): 24H2 is build 26100.

1. Before anything else, in PowerShell: `.\printpipe.exe version` prints the version, and `$LASTEXITCODE` is `0`: the manifest neither stops the program from starting nor takes its console from it.
2. `.\printpipe.exe start` says `Agent started, version X`, with no new window and no console in the taskbar. It takes no longer than a few seconds, and a stopped agent is not reported as `port 8631 is used by another program` (a closed port takes Windows about 2 s to refuse). A second `start` says `already running, version X`.
3. Close the terminal. Open another: `.\printpipe.exe test-print` still prints. In Task Manager `printpipe.exe` runs with no window.
4. `.\printpipe.exe restart` says `Agent restarted, version X`, and `/health` answers a new `pid`.
5. `.\printpipe.exe stop` says `Agent stopped` within a few seconds, and the last line of `%LOCALAPPDATA%\printpipe\agent.log` is `asked to stop`: the stop event worked. A second `stop` says `not running`. `restart` now starts it.
6. The force-kill: a build from before the stop event has none to signal. Build commit `8f9245f` for Windows, copy it to `%LOCALAPPDATA%\Programs\printpipe\printpipe.exe`, and run `serve` with it in one window. From the new build in its own folder, `stop` says `Agent stopped` within a few seconds, that window's `serve` is gone, and the log has no `asked to stop`.
7. In an `ssh` session, if the PC runs OpenSSH Server: `start` works, and the agent survives closing the session.
8. `.\printpipe.exe startup` says `off`. `startup on` says `on`, leaves the agent as it was, and `reg query HKCU\Software\Microsoft\Windows\CurrentVersion\Run /v printpipe` shows `"<path>\printpipe.exe" start`. Log out and back in: the agent runs, with no window (on a build before 24H2, a short flash is accepted; note what is seen). `startup off` leaves it running, the value is gone, and after another login it does not run. No step asked for UAC.
9. With the agent stopped, `python -m http.server 8631 --bind 127.0.0.1`: `start`, `stop` and `restart` each say `port 8631 is used by another program`, exit 1, and the server keeps running.

Checked without Windows: `make build` gives a console-subsystem binary whose `.rsrc` holds the manifest.

| date | Windows | build | result |
|---|---|---|---|
| 2026-11-04 | 11, reported as the latest: 26H2, build 26300 by Microsoft's list that day; `winver` not read | 6707db2 | From PowerShell, with the XP-365B. Step 1: `version` prints; the exit code was not recorded. Step 2: `start` says `Agent started`, a second `start` says `already running`, so a stopped agent was not taken for another program, and no window appeared. `test-print` prints, with Cyrillic in Cyrillic. Step 4: `restart` says `Agent restarted`; the log has `asked to stop` and the new agent's first line in the same second; the new `pid` was not checked. Step 5: `stop` says `Agent stopped`, the log ends with `asked to stop`, a second `stop` says `not running`; `restart` on a stopped agent was not run. Also: `serve` in a terminal, then `stop` in another, ends it through the event. `startup` says `off`, `on`, `off`. Step 3: with the terminal closed the agent keeps running and printing. Step 8: with `startup on` the agent runs after logging in, and with `startup off` it does not. With `startup on`, at logon Windows shows `Open File - Security Warning` (Unknown Publisher) for `C:\Users\Alex\Downloads\printpipe-windows-amd64.exe`: the browser's `Zone.Identifier` mark on the downloaded copy: after `Unblock-File` on it and a restart, no warning at logon, and `test-print` prints. To be fixed in `install` (#11). Not reported: step 9. Not run: steps 6 (force-kill: `serve` of this build stops through the event) and 7. |

## macOS: running in the background

Not run yet: needs a Mac, before printpipe is used on one. As an ordinary user, logged in to the desktop, with a usable `~/Library/Application Support/printpipe/config.json` and `printpipe` in `~/.local/bin` (nothing else answering on the port). `launchctl print gui/$UID/io.github.alexgred.printpipe` shows the service; below, `<plist>` is `~/Library/Application Support/printpipe/io.github.alexgred.printpipe.plist`.

1. `printpipe start` says `Agent started, version X`. `plutil -lint <plist>` accepts the plist, and it runs that binary. `launchctl print` shows `state = running`, a line `pid = N` with the `pid` of `/health`, and a line `program = <that binary>`: printpipe reads these two lines, and `launchctl print` promises no format. A second `start` says `already running, version X`: `lsof` gave the agent's program file.
2. Close the terminal and open another: `printpipe test-print` still prints.
3. `printpipe restart` says `Agent restarted, version X`, and right afterwards `/health` answers a new `pid`: `kickstart -k` returning before the old agent has left the port would report the old one.
4. `printpipe stop` says `Agent stopped`, and `launchctl print` no longer finds the service. A second `stop` says `not running`. `restart` now starts it.
5. Run `start` from a copy in another folder, then `restart` from `~/.local/bin`: `bootout` right before `bootstrap` does not fail with `Bootstrap failed: 5` (launchd may still be taking the service out), and the agent runs the binary in `~/.local/bin`.
6. `printpipe startup` says `off`. `startup on` says `on`, leaves the agent as it was, and the plist is in `~/Library/LaunchAgents`. Log out and back in: the agent runs. `printpipe stop` then says `Agent stopped`: `bootout` of the plist in Application Support finds the service loaded from LaunchAgents. `startup off` leaves a running agent running, removes the copy, and after another login it does not run. `launchctl print-disabled gui/$UID` does not list the service.
7. Kill the agent with `kill -SEGV <pid>`: launchd starts it again within about 10 s. Then break the config (remove `queue` and `device`), `stop`, `start`: after 10 s `the agent did not answer within 10 s` and the log path, and the log says `no printer` about every 10 s. `stop` says `not running`, and the lines end. Put the config back, `start`.
8. With the agent stopped, `printpipe serve` in a terminal, then `printpipe stop` in another: the agent exits, though launchd did not run it. The same with `restart`: launchd runs it afterwards.
9. With the agent stopped, `python3 -m http.server 8631 --bind 127.0.0.1`: `start`, `stop` and `restart` each say `port 8631 is used by another program`, exit 1, and the server keeps running.
10. In an `ssh` session with no desktop login: `start` reports what `launchctl` says, and nothing stays loaded.
11. `/tmp` is a link to `/private/tmp`, and `lsof` names the real path: `serve` from a copy in `/tmp`, then `stop` from the same copy. It stops the agent, or says `not this printpipe`; note which.

## macOS: through CUPS

Not run yet: needs a Mac, before printpipe is used on one. Repeat the Linux steps 1 and 2, and check that `printer.ListUSB` finds the printer: the `ioreg -r -c IOUSBHostDevice -l -w 0` output it reads is modelled on what is known of its format, not on a real Mac's output. The USB strings may also name the XP-365B as `Printer Printer-80`, as they do on Linux, since `ioreg` gives no IEEE 1284 ID. Check too that `LC_ALL=C.UTF-8 lpstat -r` says `scheduler is running`.
