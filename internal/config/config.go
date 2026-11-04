// Package config is printpipe's config.json in the user's configuration
// folder.
package config

import "encoding/json"

// DefaultPort is the port when config.json names none.
const DefaultPort = 8631

// Config is config.json. A dry run is not configured here: it is only a flag
// of the server, for development.
type Config struct {
	// Port on 127.0.0.1.
	Port int `json:"port"`
	// Origins are the pages allowed to print, such as https://app.example.
	Origins []string `json:"origins"`
	// Queue is the OS printer queue: a CUPS queue on macOS and Linux, a
	// Windows printer's name.
	Queue string `json:"queue,omitempty"`
	// Device is the printer's device, such as a Linux device file
	// /dev/usb/lp0, instead of a queue.
	Device string `json:"device,omitempty"`
}

// Default is the configuration before config.json exists.
func Default() Config { return Config{Port: DefaultPort} }

// Parse reads config.json, keeping the defaults of what it leaves out.
func Parse(b []byte) (Config, error) {
	c := Default()
	if err := json.Unmarshal(b, &c); err != nil {
		return Config{}, err
	}
	if c.Port == 0 {
		c.Port = DefaultPort
	}
	return c, nil
}
