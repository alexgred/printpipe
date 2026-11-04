# Testing

`make test` covers everything up to the OS: the HTTP server, the CLI over a fake system, the test label, and the `usb:` device setting with its error mapping. The real OS adapters are checked by hand on real machines, with the steps below, before each release.

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
| 2026-11-04 | 11 | XP-365B | 7fcc04c | Steps 3, 4 (Cyrillic prints), 5 and 6 pass. Step 7: tested with the cover open and reported fine; the exact message was not recorded. Step 8 not run: no second printer. Step 9 not reported. |
