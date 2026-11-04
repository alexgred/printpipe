# printpipe

A small agent for the computer a label printer is plugged into. Web pages in that computer's browser post raw print jobs (such as TSPL) to it at `http://127.0.0.1:8631`, and it hands the bytes to the printer untouched, without a print dialog. It knows nothing of the label layout, so a new layout never needs a new printpipe.

The same binary is the `printpipe` command and the background agent.

> Work in progress: the first release will be v0.2.0. Installing and most commands are not there yet; running in the background works on Linux only so far.

## Commands

```
printpipe help        list the commands
printpipe version     print the version (offline)
printpipe start       start the agent in the background
printpipe stop        stop the agent
printpipe restart     restart the agent, or start it when it is stopped
printpipe startup [on|off]  show, or turn on or off, starting the agent at login
printpipe test-print  print the built-in test label through the running agent
```

`start` on a running agent and `stop` on a stopped one just say so and succeed. `start` and `startup` are independent: turning starting at login on does not start the agent, and turning it off does not stop it. The agent is found only by its port, through `/health`; if another program answers there, printpipe says so and leaves it alone.

On Linux the agent is the systemd user unit `printpipe.service` in `~/.config/systemd/user/`, which runs the `printpipe` that wrote it: `start`, `stop` and `restart` are `systemctl --user`, and `startup` is `enable` and `disable`. Without a user systemd manager the agent is a detached child that `stop` ends with SIGTERM, once `/proc` shows that the process is printpipe, and starting at login is an entry in `~/.config/autostart/`.

`test-print` sends the agent's own test label (`printpipe test print`, the version, the date and time, and one Cyrillic line) to the running agent's `/print`, so it fails the way a page would. The label is made for the configured printer model's roll (the XP-365B: 58×40 mm); `--size W,H` in mm, such as `--size 40,30`, sets another.

`printpipe help` lists the commands of the coming release; the ones not there yet say so. Exit codes: 0 success, 1 any failure, 2 wrong usage.

## Configuration

`config.json` in the user's configuration folder (`%APPDATA%\printpipe\` on Windows, `~/Library/Application Support/printpipe/` on macOS, `~/.config/printpipe/` on Linux):

| key | |
|---|---|
| `port` | port on 127.0.0.1, default 8631 |
| `origins` | the pages allowed to print, such as `["https://app.example"]` |
| `queue` | the printer queue: a CUPS queue, or an existing Windows printer's name |
| `device` | a device instead of a queue: on Windows `usb:VID:PID[:serial]` in hex, such as `usb:1FC9:2016`; on Linux a device file such as `/dev/usb/lp0` |

On macOS and Linux printpipe prints through a CUPS queue with `lp -o raw`, so nothing changes the job's bytes. On Linux a `device` without `usb:` is written to directly; that needs membership of the `lp` group (`sudo usermod -aG lp $USER`, then log out and back in).

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

Go only: `make test`, `make lint`, `make fmt`, `make build` (static binaries for Windows, macOS and Linux into `dist/`). `make dev` runs the agent in dry run with `dev.json`: each job lands in `dry-run.tspl`.
