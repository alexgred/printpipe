package cli

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

// runKey is the key of the user's commands to run at logon, under HKCU.
const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

// stopEvent is what stop signals to end the agent gracefully. It is per logon
// session: stop from another session, such as ssh, finds none and ends the agent
// by force. An agent run by hand with another --config shares
// it, and either may take the request.
const stopEvent = `Local\printpipe-stop`

// environmentKey is the key of the user's environment, under HKCU, and
// pathValue its Path.
const (
	environmentKey = `Environment`
	pathValue      = "Path"
)

const (
	hwndBroadcast    = 0xffff
	wmSettingChange  = 0x001a
	smtoAbortIfHung  = 0x0002
	broadcastTimeout = 1000 // ms

	detachedProcess        = 0x00000008
	createNoWindow         = 0x08000000
	createBreakawayFromJob = 0x01000000

	eventModifyState               = 0x0002
	processQueryLimitedInformation = 0x1000
)

var (
	kernel32                  = syscall.NewLazyDLL("kernel32.dll")
	createEvent               = kernel32.NewProc("CreateEventW")
	openEvent                 = kernel32.NewProc("OpenEventW")
	setEvent                  = kernel32.NewProc("SetEvent")
	queryFullProcessImageName = kernel32.NewProc("QueryFullProcessImageNameW")
	advapi32                  = syscall.NewLazyDLL("advapi32.dll")
	regCreateKeyEx            = advapi32.NewProc("RegCreateKeyExW")
	regSetValueEx             = advapi32.NewProc("RegSetValueExW")
	regDeleteValue            = advapi32.NewProc("RegDeleteValueW")
	user32                    = syscall.NewLazyDLL("user32.dll")
	sendMessageTimeout        = user32.NewProc("SendMessageTimeoutW")
)

// Spawn starts a program with no console, so that closing the terminal does not
// end it. Leaving a job object keeps it alive when whatever started this one
// closes the job, but a job may refuse that, so it is tried without too.
func (OS) Spawn(name string, args ...string) error {
	const flags = detachedProcess | syscall.CREATE_NEW_PROCESS_GROUP
	err := spawn(flags|createBreakawayFromJob, name, args)
	if err != nil {
		err = spawn(flags, name, args)
	}
	return err
}

// spawn leaves stdin, stdout and stderr unset, which exec makes NUL. It runs in
// the program's own folder, which holds no folder of the terminal it came from.
func spawn(flags uint32, name string, args []string) error {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flags}
	cmd.Dir = filepath.Dir(name)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// RemoveAfterExit runs cmd.exe with no window: unlike a detached process, it has a
// console for ping to share, so that ping opens none. Like Spawn it leaves a job
// object if it may, or an ssh session ending would end it too. The command line is
// written out because cmd reads quotes by its own rule, which exec's escaping does not
// follow. A path with a % could be expanded by cmd, which Windows user folders
// practically never have.
func (OS) RemoveAfterExit(files, folders []string) error {
	cmdExe, err := exec.LookPath("cmd.exe")
	if err != nil {
		return err
	}
	line := `cmd.exe /d /s /c "` + removeAfterExitCommand(files, folders) + `"`
	start := func(flags uint32) error {
		cmd := exec.Command(cmdExe)
		cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: line, CreationFlags: flags}
		cmd.Dir = filepath.Dir(cmdExe) // holds none of the folders it removes
		if err := cmd.Start(); err != nil {
			return err
		}
		return cmd.Process.Release()
	}
	const flags = createNoWindow | syscall.CREATE_NEW_PROCESS_GROUP
	if start(flags|createBreakawayFromJob) == nil {
		return nil
	}
	return start(flags)
}

// removeAfterExitCommand tries each file once a second, for up to 30 s: an agent off
// its port may be finishing a job. ping is how a command waits with no keyboard.
// Once the file is gone, the rest of its loop does nothing.
func removeAfterExitCommand(files, folders []string) string {
	var steps []string
	for _, file := range files {
		steps = append(steps, fmt.Sprintf(`(for /l %%i in (1,1,30) do if exist "%[1]s" (ping -n 2 127.0.0.1 >NUL & del /f /q "%[1]s" >NUL 2>&1))`, file))
	}
	for _, folder := range folders {
		steps = append(steps, fmt.Sprintf(`rmdir "%s" >NUL 2>&1`, folder))
	}
	return strings.Join(steps, " & ")
}

// Terminate signals the stop event. Auto-reset, it wakes one agent and is off
// again, so an agent started right after does not take the same request.
func (OS) Terminate(int) error {
	name, err := syscall.UTF16PtrFromString(stopEvent)
	if err != nil {
		return err
	}
	h, _, err := openEvent.Call(eventModifyState, 0, uintptr(unsafe.Pointer(name)))
	if h == 0 {
		return err
	}
	defer syscall.CloseHandle(syscall.Handle(h))
	if r, _, err := setEvent.Call(h); r == 0 {
		return err
	}
	return nil
}

// StopRequests makes the stop event and waits on it for as long as the process
// lives, which is also how long the handle stays open.
func (OS) StopRequests() (<-chan struct{}, error) {
	name, err := syscall.UTF16PtrFromString(stopEvent)
	if err != nil {
		return nil, err
	}
	h, _, err := createEvent.Call(0, 0, 0, uintptr(unsafe.Pointer(name)))
	if h == 0 {
		return nil, err
	}
	requests := make(chan struct{})
	go func() {
		if got, _ := syscall.WaitForSingleObject(syscall.Handle(h), syscall.INFINITE); got == syscall.WAIT_OBJECT_0 {
			close(requests)
		}
	}()
	return requests, nil
}

func (OS) ProcessExe(pid int) (string, error) {
	h, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		return "", err
	}
	defer syscall.CloseHandle(h)
	// The longest path Windows has, in UTF-16 characters.
	buf := make([]uint16, 32768)
	size := uint32(len(buf))
	if r, _, err := queryFullProcessImageName.Call(uintptr(h), 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size))); r == 0 {
		return "", err
	}
	return syscall.UTF16ToString(buf[:size]), nil
}

// openRunKey opens the Run key, which a new profile may not have yet.
func openRunKey(access uint32) (syscall.Handle, error) {
	key, err := syscall.UTF16PtrFromString(runKey)
	if err != nil {
		return 0, err
	}
	var h syscall.Handle
	err = syscall.RegOpenKeyEx(syscall.HKEY_CURRENT_USER, key, 0, access, &h)
	return h, err
}

func (OS) HasRunValue(name string) bool {
	h, err := openRunKey(syscall.KEY_QUERY_VALUE)
	if err != nil {
		return false
	}
	defer syscall.RegCloseKey(h)
	value, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return false
	}
	return syscall.RegQueryValueEx(h, value, nil, nil, nil, nil) == nil
}

func (OS) SetRunValue(name, command string) error {
	return setRegistryValue(runKey, name, syscall.REG_SZ, command)
}

// setRegistryValue writes a string value of a key under HKCU, which a new
// profile may not have yet.
func setRegistryValue(keyName, name string, kind uint32, content string) error {
	key, err := syscall.UTF16PtrFromString(keyName)
	if err != nil {
		return err
	}
	value, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	data, err := syscall.UTF16FromString(content)
	if err != nil {
		return err
	}
	var h syscall.Handle
	if r, _, _ := regCreateKeyEx.Call(uintptr(syscall.HKEY_CURRENT_USER), uintptr(unsafe.Pointer(key)), 0, 0, 0,
		syscall.KEY_SET_VALUE, 0, uintptr(unsafe.Pointer(&h)), 0); r != 0 {
		return syscall.Errno(r)
	}
	defer syscall.RegCloseKey(h)
	// The size is in bytes, with the closing NUL that UTF16FromString adds.
	if r, _, _ := regSetValueEx.Call(uintptr(h), uintptr(unsafe.Pointer(value)), 0, uintptr(kind),
		uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)*2)); r != 0 {
		return syscall.Errno(r)
	}
	return nil
}

// UserPath reads the value as it is written: ExpandEnvironmentStrings is what
// would lose its %VARIABLES% on the way back.
func (OS) UserPath() (string, error) {
	key, err := syscall.UTF16PtrFromString(environmentKey)
	if err != nil {
		return "", err
	}
	value, err := syscall.UTF16PtrFromString(pathValue)
	if err != nil {
		return "", err
	}
	var h syscall.Handle
	err = syscall.RegOpenKeyEx(syscall.HKEY_CURRENT_USER, key, 0, syscall.KEY_QUERY_VALUE, &h)
	if err == syscall.ERROR_FILE_NOT_FOUND {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	defer syscall.RegCloseKey(h)
	var size uint32
	err = syscall.RegQueryValueEx(h, value, nil, nil, nil, &size)
	if err == syscall.ERROR_FILE_NOT_FOUND || (err == nil && size == 0) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	buf := make([]uint16, size/2+1)
	if err := syscall.RegQueryValueEx(h, value, nil, nil, (*byte)(unsafe.Pointer(&buf[0])), &size); err != nil {
		return "", err
	}
	return syscall.UTF16ToString(buf), nil
}

// SetUserPath writes the kind of value Windows gives the user's Path, so that
// %VARIABLES% in it go on working, and tells running programs: Explorer passes
// the new Path on to the terminals opened from now on. One that hangs is not
// waited for, and not being told is no failure.
func (OS) SetUserPath(content string) error {
	if err := setRegistryValue(environmentKey, pathValue, syscall.REG_EXPAND_SZ, content); err != nil {
		return err
	}
	if what, err := syscall.UTF16PtrFromString(environmentKey); err == nil {
		sendMessageTimeout.Call(hwndBroadcast, wmSettingChange, 0, uintptr(unsafe.Pointer(what)), smtoAbortIfHung, broadcastTimeout, 0)
	}
	return nil
}

// DeleteRunValue fails with ERROR_FILE_NOT_FOUND, which is fs.ErrNotExist, both
// for a value and for a key that is not there.
func (OS) DeleteRunValue(name string) error {
	h, err := openRunKey(syscall.KEY_SET_VALUE)
	if err != nil {
		return err
	}
	defer syscall.RegCloseKey(h)
	value, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	if r, _, _ := regDeleteValue.Call(uintptr(h), uintptr(unsafe.Pointer(value))); r != 0 {
		return syscall.Errno(r)
	}
	return nil
}
