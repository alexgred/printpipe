# printpipe

A small agent for the computer a label printer is plugged into. Web pages in that computer's browser post raw print jobs (such as TSPL) to it at `http://127.0.0.1:8631`, and it hands the bytes to the printer untouched, without a print dialog. It knows nothing of the label layout, so a new layout never needs a new printpipe.

The same binary is the `printpipe` command and the background agent.

> macOS has not yet been run on a real Mac.

## Install

Paste one line, with the address of the page allowed to print as the origin.

PowerShell on Windows:

```powershell
$env:PRINTPIPE_ORIGIN="https://app.example"; irm https://github.com/alexgred/printpipe/releases/latest/download/install.ps1 | iex
```

Terminal on macOS or Linux:

```sh
curl -fsSL https://github.com/alexgred/printpipe/releases/latest/download/install.sh | sh -s -- --origin https://app.example
```

The script downloads the build for this computer to a temp folder, checks it against `SHA256SUMS`, runs its `printpipe install` and deletes it. Everything else is done by that `install`, below. `install.sh` passes on any of its flags; `install.ps1` takes only the origin, as `irm | iex` cannot pass arguments. To run it again, paste the same line.

## Commands

```
printpipe help        list the commands
printpipe version     print the version (offline)
printpipe install [flags]   install for this user, find the printer, start and print a test label
printpipe start       start the agent in the background
printpipe stop        stop the agent
printpipe restart     restart the agent, or start it when it is stopped
printpipe startup [on|off]  show, or turn on or off, starting the agent at login
printpipe config [flags]    show the configuration, or change it with flags
printpipe upgrade     install the latest release
printpipe remove [--queue]  stop and remove the agent; --queue deletes the queue it created
printpipe status      show whether the agent runs, its version, printer and files
printpipe test-print  print the built-in test label through the running agent
```

`printpipe status` answers offline whether printing works here and where everything is: whether the agent runs and what manages it (`launchd`, `systemd` or no service manager), with its pid; the installed and running versions, suggesting `restart` when they differ; the port; whether it starts at login; the configured queue or device and whether the OS has it; and the paths of the binary, config and log. It exits with 1 when the agent is not running. The printer's paper and state are not checked.

`printpipe upgrade` installs the latest release when it is newer, and otherwise says so and succeeds, so it also answers whether there is an update. It checks the download against `SHA256SUMS`, changes only the binary, restarts the agent only if it was running, and rolls back to the previous binary if the new one does not come up. Any failure exits with 1 and leaves the agent working. It runs only from the installed binary and takes no flags.

`printpipe remove` stops the agent and removes the binary, config, log and whatever starts the agent (the systemd unit, the autostart entry, the launchd plists, the `Run` value and the `Path` entry). A printer queue stays, so that other programs can go on using it, and when printpipe created it `remove` prints the command to delete it. `remove --queue` deletes that queue too, with one `sudo`, and never a queue printpipe did not create; on Windows, where it creates none, it says so and succeeds. Run again, `remove` succeeds. On Windows the binary and the log go a moment after the command ends: a running program cannot be deleted.

`printpipe install --origin <url>` takes a computer from nothing to a running agent with a printed test label, one line per step, and can be run again at any time:

```
--origin <url>   add a page allowed to print; needed only when config.json has none
--queue <name>   print through this queue
--device <value> print to this device; on macOS and Linux usb:VID:PID[:serial] chooses the printer to make a queue for
--port <n>       the port; use only if the client's agent URL uses the same port
--size W,H       the test label's size in mm
--no-test-print  print no test label
```

It places itself in the binary folder by renames, which works over a running agent, and takes the browser's download mark off the installed file (`Zone.Identifier` on Windows, the quarantine attribute on macOS). On Windows it adds the folder to the user's `Path`; on macOS and Linux it prints the line to add when `~/.local/bin` is not on `PATH`, and edits no profile. It keeps a saved queue or device that is still there, else finds one as below. A running agent is always restarted, and the build that runs `install` is the one installed, even when older. It never turns starting at login on, but keeps what does so pointing at the installed binary. Only that binary starts by itself: `startup on`, and `start` under systemd or launchd, from a copy with nothing installed say to run `install` first.

When it stops: with no printer the configuration is written and the agent is not started, so that plugging the printer in and running the same command is the fix. A saved or given device file or Windows queue that is gone stops it too, before the agent is started; it is not replaced by a printer `install` finds. Another program on the port is left alone and the agent is not started. A test label that was not sent leaves the agent running. Each exits with 1. With several known printers connected, or on Windows several that match a `usb:VID:PID` of any model, it prints a ready command per printer.

`start` on a running agent and `stop` on a stopped one just say so and succeed. `start` and `startup` are independent: turning starting at login on does not start the agent, and turning it off does not stop it. The agent is found only by its port, through `/health`; if another program answers there, printpipe says so and leaves it alone.

On Linux the agent is the systemd user unit `printpipe.service` in `~/.config/systemd/user/`, which runs the installed `~/.local/bin/printpipe`, whichever copy wrote it: `start`, `stop` and `restart` are `systemctl --user`, and `startup` is `enable` and `disable`. Without a user systemd manager the agent is a detached child that `stop` ends with SIGTERM, once `/proc` shows that the process is printpipe, and starting at login is an entry in `~/.config/autostart/` that runs the installed binary too.

On macOS launchd owns the agent, as the service `io.github.alexgred.printpipe` in the user's session. Its plist in `~/Library/Application Support/printpipe/` runs the installed `~/.local/bin/printpipe`, whichever copy wrote it: `start` and `stop` are `launchctl bootstrap` and `bootout gui/$UID <plist>`, and `restart` is `kickstart -k`. `startup on` copies the plist to `~/Library/LaunchAgents`, and `startup off` removes the copy. launchd runs a failed agent again every 10 s or so, also one that refuses to run because of its config, until `stop`.

On Windows the agent is a process with no window and no console, which outlives the terminal it was started from. `stop` signals the event `Local\printpipe-stop` for a graceful exit, then ends the process by force, once its program file shows that it is printpipe. `startup on` adds the value `printpipe` to the user's `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`, which runs the installed `printpipe.exe`, whichever copy wrote it, with `start`; none of this needs admin rights. On Windows 11 24H2 and later nothing shows at logon; older builds may flash a console window for a moment.

`test-print` sends the agent's own test label (`printpipe test print`, the version, the date and time, and one Cyrillic line) to the running agent's `/print`, so it fails the way a page would. The label is made for the configured printer model's roll (the XP-365B: 40×30 mm); `--size W,H` in mm, such as `--size 58,40`, sets another.

`printpipe help` lists the commands. Exit codes: 0 success, 1 any failure, 2 wrong usage.

## Errors

A failure goes to stderr as `error: <what happened>`, with what to do on the lines under it. `<port>`, `<path>` and the like are filled in, and rerunning a command is always safe.

Installing, from the scripts or `printpipe upgrade`:

| `error:` | fix |
|---|---|
| `could not reach GitHub` | Check the internet connection and run the same command again. |
| `could not download <file> from GitHub`, `could not download <file> of <tag>: …` | Run the same command again. |
| `could not find the latest release on GitHub: …` | Run the same command again; if it stays, check the releases page. |
| `the download does not match SHA256SUMS: it is damaged or was tampered with` | Nothing was installed. Run the same command again; if it stays, do not use this download. |
| `SHA256SUMS does not list <file>` | Nothing was installed. The release is incomplete: try again later. |
| `printpipe has no build for <OS> <arch>` | There are builds for Windows and Linux on x86-64, and for macOS. |
| `could not make a temporary folder` | Check that the temp folder is writable and not full, then run the same command again. |
| `could not run the download in the temp folder` (`install.sh`) | It may be mounted noexec: set `TMPDIR` to another folder and run the same command again. |

The printer, from `install`:

| `error:` | fix |
|---|---|
| `no known label printer is connected` (Windows) | Plug the XP-365B in, switch it on and run the same command again. Another model: `printpipe config --device usb:VID:PID`, from the USB printers it lists. |
| `no label printer queue and no known printer found` (macOS, Linux) | Plug the XP-365B in, switch it on and run the same command again. An existing queue: `printpipe config --queue <name>`. |
| `<n> XP-365B printers are connected`, `<n> known label printers are connected`, `<n> printers match usb:VID:PID` (Windows) | Run one of the commands it prints, one per printer. A printer with no serial number, or with one another printer has too, gets no command: leave one plugged in and run the same command again. |
| `the printer <usb:…> is not connected` | Plug it in, switch it on and run the same command again. |
| `the device file <file> is not there`, `the printer queue <name> is not there` | Plug the printer in, switch it on and run the same command again. Another printer: `printpipe config --device …` or `--queue <name>`. |
| `could not check the device file <file>: …`, `could not check the printer queue <name>: …` | What the OS said is in the message. Fix that and run the same command again. |
| `<usb:…> is not a printer model printpipe knows` | An existing queue: `printpipe install --queue <name>`. |
| `there is no printer queue <name>` | See the queues with `lpstat -e` and run the same command again with one of them. |
| `CUPS is not installed (lpadmin not found)` | Install CUPS and run the same command again. |
| `CUPS does not list the connected XP-365B` | Switch the printer off and on and run the same command again, or `printpipe config --queue <name>`. |
| `could not read the printer queues: …` | Check that CUPS is running and run the same command again. |
| `could not create the printer queue: administrator rights were refused` | Run the same command again and enter your computer password when asked, or `printpipe config --queue <name>`. |
| `could not create the printer queue: …` | What `lpinfo` or `lpadmin` said is in the message. An existing queue: `printpipe config --queue <name>`. |
| `could not list the USB printers: …` | What the OS said is in the message. Fix that and run the same command again. |
| `could not place printpipe in <folder>: …` | If the agent is running: `printpipe stop`, then run the same command again. |
| `could not take the download mark off <file>: …` | In PowerShell: `Unblock-File '<file>'`, then run the same command again. |

The agent, from any command that starts or stops it:

| `error:` | fix |
|---|---|
| `port <port> is used by another program` | Close that program and run the same command again. To see which one, Windows: `netstat -ano \| findstr :<port>`; macOS and Linux: `lsof -i :<port>`. |
| `the agent did not answer within 10 s` | The reason is in the log whose path it prints. |
| `the agent (pid <pid>) did not stop within 10 s` | Run the same command again. |
| `the agent is not running` | `printpipe start`. |
| `could not check process <pid> on port <port>: …` | It may be another user's agent: stop it there. |
| `process <pid> on port <port> runs <file>, not this printpipe` | Close that program and run the same command again. |
| `printpipe is not installed, and only what is installed starts by itself` | `printpipe install --origin <url>`. |
| `<path>: …`, `<path>: no "origins": no page could print`, `<path>: port <n> is not 1 to 65535` | `config.json` cannot be used: correct or delete that file and run the same command again. |

The test label, from `install` and `test-print`. In all of these but the first the agent stays running:

| `error:` | fix |
|---|---|
| `the test label was not sent: the agent did not answer` | Check the agent: `printpipe status`. |
| `the test label was not sent: config.json allows no origin to print` | Add the page that prints: `printpipe config --origin <url>`. |
| `the test label was not sent: the printer was not found` | Check that the printer is plugged in and switched on, then `printpipe test-print`. |
| `the test label was not sent: several printers match the configured device` | Run one of the commands it prints, one per printer, or `printpipe config --device usb:VID:PID:<serial>` when it cannot list them; then `printpipe test-print`. |
| `the test label was not sent: another program is using the printer` | Close that program, then `printpipe test-print`. |
| `the test label was not sent: the printer did not respond` | Check the paper, the cover and the power, then `printpipe test-print`. |
| `the test label was not sent: the agent does not allow the configured origin` | `printpipe restart`, so that it reads `config.json`. |
| `the test label was not sent: no permission to write to <file>` | `sudo usermod -aG lp $USER`, then log out and back in. |
| `the test label was not sent: printing failed` | Fix what the agent said, printed below it, then `printpipe test-print`. |
| `the test label was not sent: the agent answered <status>`, `… the agent refused it` | Another version may be running: `printpipe status`. |

`printpipe upgrade`, `config` and `remove`:

| `error:` | fix |
|---|---|
| `this copy of printpipe is not the installed one` | Upgrade the installed one, with the command it prints. |
| `printpipe is not installed` | `printpipe install --origin <url>`. |
| `the downloaded printpipe does not run: …`, `the downloaded printpipe says it is "…", not <tag>` | Nothing changed. Try again when the next release is out. |
| `could not replace <file>: …` | Nothing changed. Fix what it says and run `printpipe upgrade` again. |
| `rolled back to <version>, log: <path>` | The old version is back, and the reason is under the line and in the log. If it says the agent did not come back: `printpipe start`. |
| `could not roll back to <version>: …` | Install again with the one-line command. |
| `cannot remove the last origin: no page could print` | Replace it: `printpipe config --origin <url> --remove-origin <old>`. |
| `could not delete the printer queue <name>: administrator rights were refused` | Run the same command again and enter your computer password when asked. |
| `could not delete the printer queue <name>: …` | What `lpadmin` said is in the message. |
| `could not remove <file>: …` | Run the same command again once that is fixed. |
| `<cause>; config.json keeps the change: …` | The agent could not restart and `config.json` could not be put back. Fix the cause, then `printpipe config` with the settings you want. |

Any command can also pass on what a program it runs said, as `systemctl: …`, `launchctl: …` or `lpstat: …`, with the program's own words: fix that and run the same command again.

Wrong usage exits with 2 and says what is wrong, with `Run printpipe help to see the commands.`, such as `unknown command`, `--origin is needed the first time: …` or `use either --queue or --device, not both`.

## Configuration

`printpipe config` shows the configuration. With flags it checks the change, writes it, says what changed and restarts a running agent as `restart` does; a stopped agent is not started, and a change that changes nothing restarts nothing.

```
--queue <name>         the printer queue, instead of a device
--device <value>       the device, instead of a queue
--origin <url>         add a page allowed to print; can be repeated, duplicates are ignored
--remove-origin <url>  remove one; can be repeated, but the last origin stays
--port <n>             the port; use only if the client's agent URL uses the same port
```

An origin is what a browser sends: the scheme and host in small letters, with no path or trailing slash, such as `https://app.example`. If the new port is taken, a running agent keeps running and nothing is written; if the agent cannot be restarted, `config.json` goes back to what it was.

The settings are in `config.json` in the user's configuration folder (`%APPDATA%\printpipe\` on Windows, `~/Library/Application Support/printpipe/` on macOS, `~/.config/printpipe/` on Linux):

| key | |
|---|---|
| `port` | port on 127.0.0.1, default 8631 |
| `origins` | the pages allowed to print, such as `["https://app.example"]` |
| `queue` | the printer queue: a CUPS queue, or an existing Windows printer's name |
| `device` | a device instead of a queue: on Windows `usb:VID:PID[:serial]` in hex, such as `usb:1FC9:2016`; on Linux a device file such as `/dev/usb/lp0` |
| `created_queue` | set by printpipe: the CUPS queue it created, the only one it may delete |

On macOS and Linux printpipe prints through a CUPS queue with `lp -o raw`, so nothing changes the job's bytes. When no usable queue exists and a known model is connected, `install` creates one, asking for your password once: only `lpinfo` and `lpadmin` run through `sudo`, never printpipe, and a queue of yours is never changed. With several identical printers connected, `install --device usb:VID:PID:<serial>` chooses the one to use: a queue that already prints to it, or a new one, whose name goes in `queue`; a `usb:` device itself is printed to only on Windows. On Linux a `device` without `usb:` is written to directly; that needs membership of the `lp` group (`sudo usermod -aG lp $USER`, then log out and back in).

On Windows printpipe writes straight to the USB printer, with no driver, no queue and no admin rights. A `usb:` device is found again on every job by its IDs, so another USB port or a replaced unit of the same model just works; the serial is needed only when several identical printers are connected (`printer_ambiguous` otherwise).

## Files

| | Windows | macOS | Linux |
|---|---|---|---|
| Binary | `%LOCALAPPDATA%\Programs\printpipe\printpipe.exe` | `~/.local/bin/printpipe` | `~/.local/bin/printpipe` |
| Config | `%APPDATA%\printpipe\config.json` | `~/Library/Application Support/printpipe/config.json` | `~/.config/printpipe/config.json` |
| Log | `%LOCALAPPDATA%\printpipe\agent.log` | `~/Library/Logs/printpipe.log` | `~/.local/state/printpipe/agent.log` |

The agent writes its own log, on stderr too when run by hand as `printpipe serve`. At about 1 MB the log becomes the one previous file, the same name with `.1` added.

## HTTP

The agent listens on `127.0.0.1` only. There is no token: the `Origin` check keeps other sites out.

- `GET /health` → `{"name": "printpipe", "version": "…", "pid": …}`, readable by any page (`Access-Control-Allow-Origin` is the requesting origin).
- `POST /print` with the job as the body (`application/octet-stream`, at most 64 KB) from one of `origins`. The job must print 1 to 10 labels in total, counted over its `PRINT m[,n]` commands as m·n. Success (`204`) means the job was **sent** to the printer; it is not followed further.

Every answer, errors included, carries `Access-Control-Allow-Origin: <requesting origin>`, so a page can always read why it did not print. Errors are JSON `{"code": "…", "message": "…"}` with an English `message`; read the `code`, the HTTP status is only a rough class:

| `code` | when | HTTP |
|---|---|---|
| `printer_not_found` | the configured device or queue is not found | 502 |
| `printer_ambiguous` | several matching USB devices | 502 |
| `printer_busy` | the device is held by another program | 502 |
| `printer_not_responding` | the write did not finish in about 10 s | 504 |
| `print_failed` | any other OS or CUPS error | 502 |
| `origin_not_allowed` | the page's origin is not in `origins` | 403 |
| `bad_job` | empty job / larger than 64 KB / not 1 to 10 labels | 400 / 413 / 422 |

## Development

Go only: `make test`, `make lint`, `make fmt`. `make build` makes static binaries for Windows, macOS and Linux, `SHA256SUMS` and the install scripts in `dist/`, and needs `sha256sum` too. `rsrc_windows_amd64.syso` is `printpipe.exe.manifest` in the form the linker embeds in the Windows binary; after changing the manifest, run `make manifest`, which needs the network once. `make dev` runs the agent in dry run with `dev.json`: each job lands in `dry-run.tspl`.

To release: move the changes in `CHANGELOG.md` under `## vX.Y.Z`, commit, `git tag vX.Y.Z`, `git push origin main vX.Y.Z`, `make release`. It runs `make lint`, `make test` and `make build`, and publishes `dist/` with `gh`, with that section as the notes. No binaries go into git.
