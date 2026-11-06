package printer

import (
	"context"
	"errors"
	"io/fs"
	"os/exec"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// fakeComputer answers reads from files and programs by their whole command.
type fakeComputer struct {
	Computer
	goos     string
	files    map[string]string
	commands map[string]string
}

func (c fakeComputer) GOOS() string { return c.goos }

func (c fakeComputer) ReadFile(name string) ([]byte, error) {
	b, ok := c.files[name]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return []byte(b), nil
}

func (c fakeComputer) ReadDir(name string) ([]string, error) {
	var names []string
	for path := range c.files {
		if rest, ok := strings.CutPrefix(path, name+"/"); ok {
			entry, _, _ := strings.Cut(rest, "/")
			if !slices.Contains(names, entry) {
				names = append(names, entry)
			}
		}
	}
	if names == nil {
		return nil, fs.ErrNotExist
	}
	slices.Sort(names)
	return names, nil
}

func (c fakeComputer) Run(_ context.Context, _ []byte, name string, args ...string) ([]byte, error) {
	out, ok := c.commands[strings.Join(append([]string{name}, args...), " ")]
	if !ok {
		return nil, &exec.Error{Name: name, Err: exec.ErrNotFound}
	}
	return []byte(out), nil
}

func TestQueuesReadsTheirDeviceAndStateNotTheLocalizedList(t *testing.T) {
	c := fakeComputer{commands: map[string]string{
		"lpstat -r": "scheduler is running\n",
		"lpstat -e": "XP-365B\nGone\nOffice\n",
		"lpoptions -p XP-365B": "copies=1 device-uri=usb://Xprinter/XP-365B?serial=0020426981AA finishings=3 printer-info=XP-365B " +
			"printer-is-accepting-jobs=true printer-location printer-make-and-model='Generic Text-Only Printer' printer-state=3\n",
		"lpoptions -p Gone":   "\n",
		"lpoptions -p Office": `device-uri=ipp://office.local/ipp/print printer-is-accepting-jobs=false printer-make-and-model=Bob\'s\ Laser` + "\n",
	}}
	got, err := Queues(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	want := []QueueInfo{
		{Name: "XP-365B", DeviceURI: "usb://Xprinter/XP-365B?serial=0020426981AA", MakeAndModel: "Generic Text-Only Printer", Accepting: true},
		{Name: "Gone"},
		{Name: "Office", DeviceURI: "ipp://office.local/ipp/print", MakeAndModel: "Bob's Laser"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
}

// With the scheduler down lpstat -e lists nothing and exits 0, which is not the
// same as having no queues: install must not create one then.
func TestQueuesWithTheSchedulerDownFails(t *testing.T) {
	c := fakeComputer{commands: map[string]string{"lpstat -r": "scheduler is not running\n", "lpstat -e": ""}}
	if got, err := Queues(context.Background(), c); err == nil {
		t.Errorf("got %+v, want an error", got)
	}
}

func TestQueuesWithoutCUPSSaysSo(t *testing.T) {
	if _, err := Queues(context.Background(), fakeComputer{}); !errors.Is(err, exec.ErrNotFound) {
		t.Errorf("got %v, want exec.ErrNotFound", err)
	}
}
