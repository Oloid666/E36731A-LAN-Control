package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type Preferences struct {
	IP      string            `json:"ip"`
	Config  Config            `json:"config"`
	Options ConnectionOptions `json:"options"`
}

func DefaultPreferences() Preferences {
	return Preferences{IP: "169.254.30.1", Config: Config{Role: "battery", Mode: "CV", Voltage: 3.7, Current: .1, OVP: 4.3, Resistance: 100, ESRMilliohm: 0, MinVoltage: 3.5, MaxVoltage: 4.2}, Options: DefaultOptions()}
}
func (p Preferences) validate() error {
	if e := validIP(p.IP); e != nil {
		return e
	}
	if e := p.Config.validate(); e != nil {
		return e
	}
	_, e := p.Options.normalized()
	return e
}
func (c *Controller) loadPreferences() {
	c.state.Preferences = DefaultPreferences()
	if c.preferencesPath == "" {
		return
	}
	bytes, e := os.ReadFile(c.preferencesPath)
	if os.IsNotExist(e) {
		return
	}
	if e != nil {
		c.log("SETTINGS load failed: " + e.Error())
		return
	}
	if len(bytes) > 65536 {
		c.log("SETTINGS file too large; defaults used")
		return
	}
	var p Preferences
	if json.Unmarshal(bytes, &p) != nil {
		c.log("SETTINGS invalid JSON; defaults used")
		return
	}
	if p.Config.Mode == "ESR" || p.Config.ESRMilliohm != 0 {
		if p.Config.Role == "battery" {
			p.Config.Mode = "CV"
		}
		p.Config.ESRMilliohm = 0
		c.log("SETTINGS migrated: software ESR removed; native ESR not configured; no instrument commands sent")
	}
	if p.Config.Role != "battery" {
		p.Config.Role, p.Config.Mode = "battery", "CV"
		if p.Config.MinVoltage > p.Config.Voltage {
			p.Config.MinVoltage = 0
		}
		if p.Config.MaxVoltage < p.Config.Voltage {
			p.Config.MaxVoltage = p.Config.Voltage
		}
		if p.Config.OVP <= p.Config.MaxVoltage {
			p.Config.OVP = p.Config.MaxVoltage + .1
		}
		c.log("SETTINGS defaulted to battery PS2Q CV; values retained when valid; no commands sent")
	}
	if p.validate() != nil {
		c.log("SETTINGS invalid; defaults used")
		return
	}
	p.Options, _ = p.Options.normalized()
	c.state.Preferences = p
	c.state.Options = p.Options
	c.log("SETTINGS loaded; instrument not connected, not configured, output not enabled")
}
func (c *Controller) savePreferences(p Preferences) error {
	if e := p.validate(); e != nil {
		return e
	}
	p.Options, _ = p.Options.normalized()
	c.state.Preferences = p
	if c.preferencesPath == "" {
		return fmt.Errorf("設定已在本次程式記憶，但本機儲存位置不可用")
	}
	bytes, e := json.MarshalIndent(p, "", "  ")
	if e != nil {
		return e
	}
	temp := c.preferencesPath + ".tmp"
	if e = os.WriteFile(temp, bytes, 0600); e != nil {
		return e
	}
	// Rename over an existing destination is not portable to all Windows filesystems.
	// The temp file is complete first; failure preserves explicit error reporting.
	if e = os.Rename(temp, c.preferencesPath); e != nil {
		if e = os.WriteFile(c.preferencesPath, bytes, 0600); e != nil {
			return e
		}
		os.Remove(temp)
	}
	c.log("SETTINGS saved; no instrument commands sent by saving")
	return nil
}
func setupStorage(c *Controller) func() {
	c.loadPreferences()
	base, e := os.UserConfigDir()
	if e != nil {
		c.log("User config folder unavailable: " + e.Error())
		return func() {}
	}
	dir := filepath.Join(base, "E36731A-LAN-Control")
	if e = os.MkdirAll(dir, 0700); e != nil {
		c.log("User config folder not writable: " + e.Error())
		return func() {}
	}
	c.preferencesPath = filepath.Join(dir, "preferences.json")
	logfile, e := os.OpenFile(filepath.Join(dir, "debug.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if e != nil {
		c.log("Persistent log unavailable: " + e.Error())
		c.loadPreferences()
		return func() {}
	}
	// Keep disk logs bounded between sessions. In-memory export also keeps 2000 lines.
	if stat, e := logfile.Stat(); e == nil && stat.Size() > 5*1024*1024 {
		logfile.Close()
		os.Remove(filepath.Join(dir, "debug.previous.log"))
		os.Rename(filepath.Join(dir, "debug.log"), filepath.Join(dir, "debug.previous.log"))
		logfile, _ = os.OpenFile(filepath.Join(dir, "debug.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	}
	if logfile != nil {
		c.logWriter = logfile
	}
	c.loadPreferences()
	c.log("START " + c.state.Version + "; software ESR removed; native ESR not configured; outputs remain under operator control")
	return func() {
		if logfile != nil {
			logfile.Close()
		}
	}
}
