package main

import (
	"encoding/json"
	"fmt"
	"math"
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
	return Config{Role: "battery", Mode: "ESR", Voltage: 3.7, Current: 1, OVP: 4.3, ESRMilliohm: 10, MinVoltage: 3.5, MaxVoltage: 4.2}
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
func TestESRFormulaBothDirections(t *testing.T) {
	p := batteryConfig()
	for _, test := range []struct{ i, v float64 }{{1, 3.69}, {-1, 3.71}, {0, 3.7}} {
		v, e := esrTarget(p, test.i)
		if e != nil || math.Abs(v-test.v) > 1e-9 {
			t.Fatal(test, e, v)
		}
	}
}
func TestESRRejectsCurrentAndVoltageOutsideModel(t *testing.T) {
	p := batteryConfig()
	if _, e := esrTarget(p, 2); e == nil {
		t.Fatal("overcurrent accepted")
	}
	p.ESRMilliohm = 1000
	if _, e := esrTarget(p, 1); e == nil {
		t.Fatal("target outside voltage range accepted")
	}
}
func TestESRValidationRejectsUnsafeRanges(t *testing.T) {
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
func TestESRApplyUsesPS2QNotLoadRES(t *testing.T) {
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
func TestESRFeedbackFollowsMeasuredDirection(t *testing.T) {
	c, f := setup(t)
	p := batteryConfig()
	if e := c.apply(p); e != nil {
		t.Fatal(e)
	}
	discharge := 1.0
	f.mu.Lock()
	f.measuredCurrent = &discharge
	f.mu.Unlock()
	if e := c.on(p); e != nil {
		t.Fatal(e)
	}
	if math.Abs(c.esrCommand-3.695) > 1e-9 {
		t.Fatal("discharge step", c.esrCommand)
	}
	charge := -1.0
	f.mu.Lock()
	f.measuredCurrent = &charge
	f.mu.Unlock()
	if e := c.sample(); e != nil {
		t.Fatal(e)
	}
	if math.Abs(c.esrCommand-3.7) > 1e-9 {
		t.Fatal("charge step", c.esrCommand)
	}
	if c.state.Reading.ESRTarget == nil || math.Abs(*c.state.Reading.ESRTarget-3.71) > 1e-9 {
		t.Fatal("model target missing")
	}
}
func TestESRNoFeedbackAfterOff(t *testing.T) {
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
	if c.state.Config != nil || c.state.Reading.ESRTarget != nil {
		t.Fatal("ESR remained armed")
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
	if fresh.state.Preferences.IP != "169.254.30.1" || fresh.state.Preferences.Config.ESRMilliohm != 10 {
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
	if json.Unmarshal(w.Body.Bytes(), &value) != nil || value.Version != "v0.3.1" {
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
