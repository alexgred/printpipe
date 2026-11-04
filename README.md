# printpipe

A small agent for the computer a label printer is plugged into. Web pages in that computer's browser post raw print jobs (such as TSPL) to it at `http://127.0.0.1:8631`, and it hands the bytes to the printer untouched, without a print dialog. It knows nothing of the label layout, so a new layout never needs a new printpipe.

The same binary is the `printpipe` command and the background agent.

> Work in progress: the first release will be v0.2.0. `upgrade`, `remove` and the download scripts are not there yet; running in the background works on Linux and Windows, and on macOS it has not yet been run on a real Mac.

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
printpipe status      show whether the agent runs, its version, printer and files
printpipe test-print  print the built-in test label through the running agent
```

`printpipe status` answers offline whether printing works here and where everything is: whether the agent runs and what manages it (`launchd`, `systemd` or no service manager), with its pid; the installed and running versions, suggesting `restart` when they differ; the port; whether it starts at login; the configured queue or device and whether the OS has it; and the paths of the binary, config and log. It exits with 1 when the agent is not running. The printer's paper and state are not checked.

`printpipe install --origin <url>` takes a computer from nothing to a running agent with a printed test label, one line per step, and can be run again at any time:

```
--origin <url>   add a page allowed to print; needed only when config.json has none
--queue <name>   print through this queue
--device <value> print to this device; on macOS and Linux usb:VID:PID[:serial] chooses the printer to make a queue for
--port <n>       the port
--size W,H       the test label's size in mm
--no-test-print  print no test label
```

It places itself in the binary folder by renames, which works over a running agent, and takes the browser's download mark off the installed file (`Zone.Identifier` on Windows, the quarantine attribute on macOS). On Windows it adds the folder to the user's `Path`; on macOS and Linux it prints the line to add when `~/.local/bin` is not on `PATH`, and edits no profile. It keeps a saved queue or device that is still there, else finds one as below. A running agent is always restarted, and the build that runs `install` is the one installed, even when older. It never turns starting at login on, but keeps what does so pointing at the installed binary. Only that binary starts by itself: `startup on`, and `start` under systemd or launchd, from a copy with nothing installed say to run `install` first.

When it stops: with no printer the configuration is written and the agent is not started, so that plugging the printer in and running the same command is the fix. Another program on the port is left alone and the agent is not started. A test label that was not sent leaves the agent running. Each exits with 1. With several known printers connected it prints a ready command per printer.

`start` on a running agent and `stop` on a stopped one just say so and succeed. `start` and `startup` are independent: turning starting at login on does not start the agent, and turning it off does not stop it. The agent is found only by its port, through `/health`; if another program answers there, printpipe says so and leaves it alone.

On Linux the agent is the systemd user unit `printpipe.service` in `~/.config/systemd/user/`, which runs the installed `~/.local/bin/printpipe`, whichever copy wrote it: `start`, `stop` and `restart` are `systemctl --user`, and `startup` is `enable` and `disable`. Without a user systemd manager the agent is a detached child that `stop` ends with SIGTERM, once `/proc` shows that the process is printpipe, and starting at login is an entry in `~/.config/autostart/` that runs the installed binary too.

On macOS launchd owns the agent, as the service `io.github.alexgred.printpipe` in the user's session. Its plist in `~/Library/Application Support/printpipe/` runs the installed `~/.local/bin/printpipe`, whichever copy wrote it: `start` and `stop` are `launchctl bootstrap` and `bootout gui/$UID <plist>`, and `restart` is `kickstart -k`. `startup on` copies the plist to `~/Library/LaunchAgents`, and `startup off` removes the copy. launchd runs a failed agent again every 10 s or so, also one that refuses to run because of its config, until `stop`.

On Windows the agent is a process with no window and no console, which outlives the terminal it was started from. `stop` signals the event `Local\printpipe-stop` for a graceful exit, then ends the process by force, once its program file shows that it is printpipe. `startup on` adds the value `printpipe` to the user's `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`, which runs the installed `printpipe.exe`, whichever copy wrote it, with `start`; none of this needs admin rights. On Windows 11 24H2 and later nothing shows at logon; older builds may flash a console window for a moment.

`test-print` sends the agent's own test label (`printpipe test print`, the version, the date and time, and one Cyrillic line) to the running agent's `/print`, so it fails the way a page would. The label is made for the configured printer model's roll (the XP-365B: 58×40 mm); `--size W,H` in mm, such as `--size 40,30`, sets another.

`printpipe help` lists the commands of the coming release; the ones not there yet say so. Exit codes: 0 success, 1 any failure, 2 wrong usage.

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

Go only: `make test`, `make lint`, `make fmt`, `make build` (static binaries for Windows, macOS and Linux into `dist/`). `rsrc_windows_amd64.syso` is `printpipe.exe.manifest` in the form the linker embeds in the Windows binary; after changing the manifest, run `make manifest`, which needs the network once. `make dev` runs the agent in dry run with `dev.json`: each job lands in `dry-run.tspl`.
