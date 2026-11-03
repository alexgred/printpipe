# Windows: direct USB, not a queue

On Windows printpipe writes the job straight to the printer's usbprint device interface (`{28d78fad-5a12-11d1-ae5b-0000f803a8c2}`, `CreateFile`/`WriteFile`), found by VID:PID through SetupAPI on every job, instead of printing through a Windows printer queue. Creating a queue needs administrator rights (a UAC prompt) and a driver for the model, which an ordinary user installing from a web page has neither of; the usbprint interface is there as soon as the printer is plugged in, readable and writable by the user, and takes raw TSPL as it is. Finding the device again on every job by its IDs (`usb:1FC9:2016`, a serial only when two are connected) means a different USB port or a replaced unit of the same model just works.

## Consequences

- printpipe has no Windows queue-creation code; an existing queue is used only when the user names it as `queue` in the config (the spooler path with the RAW datatype).
- printpipe holds the device exclusively while writing, so it fails as busy when another program (such as a vendor driver's own port monitor) has it open; whether a vendor driver does that is to be checked on real machines.
- Nothing follows the job after the write: printing with no paper still succeeds, as with a queue.
