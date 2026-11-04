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

## macOS: through CUPS

Not run yet: needs a Mac, before printpipe is used on one. Repeat the Linux steps 1 and 2, and check that `printer.ListUSB` finds the printer: the `ioreg -r -c IOUSBHostDevice -l -w 0` output it reads is modelled on what is known of its format, not on a real Mac's output. The USB strings may also name the XP-365B as `Printer Printer-80`, as they do on Linux, since `ioreg` gives no IEEE 1284 ID. Check too that `LC_ALL=C.UTF-8 lpstat -r` says `scheduler is running`.
