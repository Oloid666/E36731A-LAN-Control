package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func unconnectedFake(t *testing.T) (*Controller, *fakeInstrument) {
	t.Helper()
	f := fake()
	c := NewController()
	c.dial = func(_, _ string, _ time.Duration) (net.Conn, error) { a, b := net.Pipe(); go f.serve(b); return a, nil }
	t.Cleanup(func() {
		if c.conn != nil {
			c.conn.Close()
		}
	})
	return c, f
}
func batteryConfig() Config {
	return Config{Role: "battery", Mode: "CV", Voltage: 3.7, Current: 1, OVP: 4.3, ESRMilliohm: 0, MinVoltage: 3.5, MaxVoltage: 4.2}
}
func TestConnectionBaseline305DoesNotDisconnect(t *testing.T) {
	c, f := unconnectedFake(t)
	f.errors = []string{`+305,"Requested operation is unsupported in current mode"`}
	c.begin()
	if e := c.connect("169.254.30.1"); e != nil {
		t.Fatal(e)
	}
	if !c.state.Connected || !strings.Contains(c.state.Warning, "305") {
		t.Fatal("baseline not surfaced")
	}
	if has(f, "*CLS") || has(f, "OUTP ON") || has(f, "OUTP OFF") {
		t.Fatal("baseline caused hardware write")
	}
}
func TestFresh305StillFailsAfterCleanBaseline(t *testing.T) {
	c, f := unconnectedFake(t)
	f.errorOn = map[string]string{"MEAS:VOLT?": `+305,"Requested operation is unsupported in current mode"`}
	c.begin()
	e := c.connect("169.254.30.1")
	if e == nil || !strings.Contains(e.Error(), "305") {
		t.Fatal("fresh error ignored", e)
	}
	c.fail(e)
	if c.state.Connected || c.state.Error == "" {
		t.Fatal("fresh error did not stop session")
	}
	if c.state.Unknown {
		t.Fatal("clean OFF acknowledgement should be recognized")
	}
}
func TestDiagnosticConnectDoesNotPollOrEnable(t *testing.T) {
	c, f := unconnectedFake(t)
	c.state.DebugOnly = true
	f.errorOn = map[string]string{"MEAS:VOLT?": `+305,"unsupported"`}
	c.begin()
	if e := c.connect("169.254.30.1"); e != nil {
		t.Fatal(e)
	}
	if has(f, "MEAS:VOLT?") {
		t.Fatal("debug connect polled")
	}
	if c.on(config("source", "CV")) == nil {
		t.Fatal("debug mode allowed ON")
	}
	if e := c.diagnose(); e != nil {
		t.Fatal(e)
	}
	found := false
	for _, row := range c.state.Diagnostics {
		if row.Command == "MEAS:VOLT?" && strings.Contains(row.Error, "305") {
			found = true
		}
	}
	if !found {
		t.Fatal("diagnostic did not attribute error to command")
	}
	if has(f, "OUTP ON") {
		t.Fatal("diagnostic enabled hardware")
	}
}
func TestNativeESRRequestRejectedBeforeAnyCommand(t *testing.T) {
	c, f := setup(t)
	for _, esr := range []float64{10, 100, -10} {
		p := batteryConfig()
		p.ESRMilliohm = esr
		f.mu.Lock()
		before := len(f.commands)
		f.mu.Unlock()
		if err := c.apply(p); err == nil || !strings.Contains(err.Error(), "硬體 ESR") {
			t.Fatal("unsupported native ESR accepted", err)
		}
		f.mu.Lock()
		after := len(f.commands)
		f.mu.Unlock()
		if before != after {
			t.Fatal("ESR request sent instrument commands")
		}
	}
}
func TestLegacySoftwareESRModeRejected(t *testing.T) {
	p := batteryConfig()
	p.Mode = "ESR"
	if p.validate() == nil {
		t.Fatal("removed software mode accepted")
	}
}
func TestPS2QValidationRejectsUnsafeRanges(t *testing.T) {
	cases := []Config{}
	p := batteryConfig()
	p.MinVoltage = 4
	cases = append(cases, p)
	p = batteryConfig()
	p.MaxVoltage = 4.3
	cases = append(cases, p)
	p = batteryConfig()
	p.ESRMilliohm = -10
	cases = append(cases, p)
	p = batteryConfig()
	p.Current = .001
	cases = append(cases, p)
	for _, p := range cases {
		if p.validate() == nil {
			t.Fatalf("invalid model accepted %+v", p)
		}
	}
}
func TestPS2QApplyUsesNativeModeWithoutRES(t *testing.T) {
	c, f := setup(t)
	p := batteryConfig()
	if e := c.apply(p); e != nil {
		t.Fatal(e)
	}
	if !has(f, "EMUL PS2Q") {
		t.Fatal("no PS2Q")
	}
	for _, cmd := range []string{"FUNC RES", "RES 0.01", "VOLT:MODE FIX", "OUTP:PMOD VOLT", "OUTP ON"} {
		if has(f, cmd) {
			t.Fatal("wrong command in ESR apply", cmd)
		}
	}
}
func TestPS2QRejectionStopsBeforeOn(t *testing.T) {
	c, f := setup(t)
	f.mu.Lock()
	f.rejectPS2Q = true
	f.mu.Unlock()
	if e := c.apply(batteryConfig()); e == nil || !strings.Contains(e.Error(), "305") {
		t.Fatal(e)
	}
	if has(f, "OUTP ON") || c.state.Config != nil {
		t.Fatal("unsupported PS2Q armed")
	}
}
func TestPS2QSamplesDoNotAdjustVoltageInEitherDirection(t *testing.T) {
	c, f := setup(t)
	p := batteryConfig()
	if err := c.apply(p); err != nil {
		t.Fatal(err)
	}
	if err := c.on(p); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	before := len(f.commands)
	f.mu.Unlock()
	for _, i := range []float64{1, -1, 0, .5, -.5} {
		current := i
		f.mu.Lock()
		f.measuredCurrent = &current
		f.mu.Unlock()
		if err := c.sample(); err != nil {
			t.Fatal(err)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, cmd := range f.commands[before:] {
		if !strings.Contains(cmd, "?") {
			t.Fatal("monitoring wrote to hardware", cmd)
		}
	}
	if f.values["VOLT"] != "3.7" {
		t.Fatal("voltage drifted")
	}
}
func TestPS2QBidirectionalCurrentMonitorStopsOverLimit(t *testing.T) {
	for _, i := range []float64{2, -2} {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			c, f := setup(t)
			p := batteryConfig()
			if err := c.apply(p); err != nil {
				t.Fatal(err)
			}
			if err := c.on(p); err != nil {
				t.Fatal(err)
			}
			f.mu.Lock()
			f.measuredCurrent = &i
			f.mu.Unlock()
			err := c.sample()
			if err == nil || !strings.Contains(err.Error(), "雙向軟體電流") {
				t.Fatal("overcurrent ignored", err)
			}
			c.fail(err)
			if c.state.Unknown || c.state.Config != nil {
				t.Fatal("OFF not confirmed")
			}
		})
	}
}
func TestPS2QNoMonitoringAfterOff(t *testing.T) {
	c, f := setup(t)
	p := batteryConfig()
	if e := c.apply(p); e != nil {
		t.Fatal(e)
	}
	if e := c.off(); e != nil {
		t.Fatal(e)
	}
	if e := c.sample(); e != nil {
		t.Fatal(e)
	}
	if c.state.Config != nil {
		t.Fatal("PS2Q remained armed")
	}
	if has(f, "OUTP ON") {
		t.Fatal("unexpected ON")
	}
}
func TestCleanErrorConfirmsOffButTimeoutDoesNot(t *testing.T) {
	c, f := setup(t)
	c.fail(fmt.Errorf("settings readback mismatch"))
	if c.state.Unknown || c.state.Connected {
		t.Fatal("clean OFF not confirmed")
	}
	if !has(f, "OUTP OFF") {
		t.Fatal("OFF missing")
	}
}
func TestPreferencesPersistWithoutHardwareCommands(t *testing.T) {
	c, f := setup(t)
	c.preferencesPath = filepath.Join(t.TempDir(), "preferences.json")
	p := DefaultPreferences()
	p.Config = batteryConfig()
	f.mu.Lock()
	before := len(f.commands)
	f.mu.Unlock()
	if e := c.savePreferences(p); e != nil {
		t.Fatal(e)
	}
	f.mu.Lock()
	after := len(f.commands)
	f.mu.Unlock()
	if before != after {
		t.Fatal("save sent commands")
	}
	fresh := NewController()
	fresh.preferencesPath = c.preferencesPath
	fresh.loadPreferences()
	if fresh.state.Preferences.IP != "169.254.30.1" || fresh.state.Preferences.Config.ESRMilliohm != 0 {
		t.Fatal("settings lost")
	}
	if fresh.conn != nil || fresh.state.Config != nil {
		t.Fatal("startup connected or armed hardware")
	}
}
func TestMalformedPreferencesFallBackToDefaults(t *testing.T) {
	c := NewController()
	c.preferencesPath = filepath.Join(t.TempDir(), "preferences.json")
	os.WriteFile(c.preferencesPath, []byte(`{"ip":"127.0.0.1"}`), 0600)
	c.loadPreferences()
	if c.state.Preferences.IP != "169.254.30.1" || c.state.Config != nil {
		t.Fatal("unsafe preferences restored")
	}
}
func TestDebugExportOmitsSessionSecret(t *testing.T) {
	c, _ := setup(t)
	s := &Server{controller: c, token: "secret-session", host: "localhost"}
	r := httptest.NewRequest("GET", "http://localhost/api/debug", nil)
	r.Header.Set("X-Control-Token", s.token)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 200 || strings.Contains(w.Body.String(), s.token) {
		t.Fatal(w.Code)
	}
	var value State
	if json.Unmarshal(w.Body.Bytes(), &value) != nil || value.Version != "v0.4.0" {
		t.Fatal("export missing version")
	}
}
func TestConnectionOptionsAreBounded(t *testing.T) {
	if _, e := (ConnectionOptions{TimeoutMS: 999999, PollMS: 1}).normalized(); e == nil {
		t.Fatal("unbounded options accepted")
	}
	got, e := (ConnectionOptions{}).normalized()
	if e != nil || got != DefaultOptions() {
		t.Fatal(got, e)
	}
}

func TestLegacyPreferencesMigrateWithoutHardwareESRClaim(t *testing.T) {
	c := NewController()
	c.preferencesPath = filepath.Join(t.TempDir(), "preferences.json")
	p := DefaultPreferences()
	p.Config = batteryConfig()
	p.Config.Mode = "ESR"
	p.Config.ESRMilliohm = 100
	b, _ := json.Marshal(p)
	if err := os.WriteFile(c.preferencesPath, b, 0600); err != nil {
		t.Fatal(err)
	}
	c.loadPreferences()
	got := c.state.Preferences.Config
	if got.Mode != "CV" || got.ESRMilliohm != 0 || c.conn != nil || c.state.Config != nil {
		t.Fatalf("unsafe migration: %+v", got)
	}
	if !strings.Contains(strings.Join(c.state.Logs, "\n"), "software ESR removed") {
		t.Fatal("migration not logged")
	}
}
