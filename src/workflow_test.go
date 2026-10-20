package main

import (
	"encoding/json"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func testRequest(s *Server, action string, p Config) *httptest.ResponseRecorder {
	b, _ := json.Marshal(Action{Action: action, IP: "169.254.30.1", Config: p, Options: DefaultOptions()})
	r := httptest.NewRequest("POST", "http://localhost/api/action", strings.NewReader(string(b)))
	r.Header.Set("X-Control-Token", s.token)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

func TestOneClickPrepareConnectsSwitchesAndLeavesOff(t *testing.T) {
	c, f := unconnectedFake(t)
	s := &Server{controller: c, host: "localhost", token: "s"}
	w := testRequest(s, "prepare", batteryConfig())
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if !c.state.Connected || c.state.Config == nil || c.state.Reading.Emulation != "PS2Q" || c.state.Reading.Output || has(f, "OUTP ON") {
		t.Fatal("prepare did not establish OFF PS2Q")
	}
}

func TestOneClickPrepareTurnsOffBeforeChangingVoltage(t *testing.T) {
	c, f := setup(t)
	p := batteryConfig()
	c.begin()
	if e := c.apply(p); e != nil {
		t.Fatal(e)
	}
	if e := c.on(p); e != nil {
		t.Fatal(e)
	}
	f.mu.Lock()
	before := len(f.commands)
	f.mu.Unlock()
	p.Voltage = 3.8
	c.begin()
	if e := c.prepare(c.state.IP, p, DefaultOptions()); e != nil {
		t.Fatal(e)
	}
	f.mu.Lock()
	commands := strings.Join(f.commands[before:], "\n")
	f.mu.Unlock()
	if strings.Index(commands, "OUTP OFF") < 0 || strings.Index(commands, "OUTP OFF") > strings.Index(commands, "VOLT 3.8") || strings.Contains(commands, "OUTP ON") || c.state.Reading.Output {
		t.Fatal(commands)
	}
}

func TestOffCanBeFollowedByOnWithoutReapplyButChecksChanges(t *testing.T) {
	c, f := setup(t)
	p := batteryConfig()
	c.begin()
	if e := c.apply(p); e != nil {
		t.Fatal(e)
	}
	if e := c.on(p); e != nil {
		t.Fatal(e)
	}
	if e := c.off(); e != nil {
		t.Fatal(e)
	}
	if e := c.on(p); e != nil {
		t.Fatal(e)
	}
	if e := c.off(); e != nil {
		t.Fatal(e)
	}
	f.mu.Lock()
	f.values["CURR"] = "8"
	before := len(f.commands)
	f.mu.Unlock()
	if c.on(p) == nil {
		t.Fatal("changed current accepted")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if strings.Contains(strings.Join(f.commands[before:], "\n"), "OUTP ON") {
		t.Fatal("ON sent after mismatch")
	}
}

func TestOffDuringSlowOPCPreservesConnectionAndStateEndpoint(t *testing.T) {
	c, f := setup(t)
	c.state.IP = "169.254.30.1"
	release := make(chan struct{})
	entered := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	f.mu.Lock()
	f.gateCommand = "*OPC?"
	f.gateEntered = entered
	f.gateRelease = release
	f.mu.Unlock()
	s := &Server{controller: c, host: "localhost", token: "s"}
	applied := make(chan *httptest.ResponseRecorder, 1)
	go func() { applied <- testRequest(s, "prepare", batteryConfig()) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("did not reach OPC")
	}
	// HTTP state must return progress despite the SCPI mutex being held.
	gotState := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		r := httptest.NewRequest("GET", "http://localhost/api/state", nil)
		r.Header.Set("X-Control-Token", "s")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		gotState <- w
	}()
	select {
	case w := <-gotState:
		var state struct {
			Busy        bool   `json:"busy"`
			LastCommand string `json:"last_command"`
		}
		if json.Unmarshal(w.Body.Bytes(), &state) != nil || !state.Busy || state.LastCommand != "*OPC?" {
			t.Fatal(w.Body.String())
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("state endpoint blocked on OPC")
	}
	stopped := make(chan *httptest.ResponseRecorder, 1)
	go func() { stopped <- testRequest(s, "off", Config{}) }()
	until := time.Now().Add(time.Second)
	for c.emergencies.Load() == 0 && time.Now().Before(until) {
		time.Sleep(time.Millisecond)
	}
	if c.emergencies.Load() == 0 {
		t.Fatal("OFF not queued")
	}
	once.Do(func() { close(release) })
	for _, done := range []chan *httptest.ResponseRecorder{applied, stopped} {
		select {
		case w := <-done:
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
		case <-time.After(3 * time.Second):
			t.Fatal("operation did not finish")
		}
	}
	if !c.state.Connected || c.conn == nil || c.state.Unknown || c.state.Reading.Output || c.state.Config != nil || c.state.Error != "" {
		t.Fatalf("cancel became connection failure: %+v", c.state)
	}
	w := testRequest(s, "prepare", batteryConfig())
	if w.Code != 200 {
		t.Fatal("could not prepare again", w.Body.String())
	}
	if has(f, "OUTP ON") {
		t.Fatal("cancel/retry enabled output")
	}
}

func TestSnapshotOwnsMutableState(t *testing.T) {
	c := NewController()
	c.state.Config = &Config{Voltage: 3.7}
	c.state.Reading = &Reading{Voltage: 3.7}
	c.state.Logs = []string{"old"}
	c.publish()
	old := c.snapshot.Load()
	c.state.Config.Voltage = 8
	c.state.Reading.Voltage = 8
	c.state.Logs[0] = "new"
	if old.Config.Voltage != 3.7 || old.Reading.Voltage != 3.7 || old.Logs[0] != "old" {
		t.Fatal("published snapshot mutated")
	}
}

func TestDefaultAndSavedSourceProfileStartAsBattery(t *testing.T) {
	if DefaultPreferences().Config.Role != "battery" {
		t.Fatal("default not battery")
	}
	c := NewController()
	c.preferencesPath = filepath.Join(t.TempDir(), "preferences.json")
	p := DefaultPreferences()
	p.Config.Role = "source"
	p.Config.Voltage = 11.1
	p.Config.OVP = 12.9
	p.Config.MinVoltage = 10.5
	p.Config.MaxVoltage = 12.6
	b, _ := json.Marshal(p)
	if e := os.WriteFile(c.preferencesPath, b, 0600); e != nil {
		t.Fatal(e)
	}
	c.loadPreferences()
	got := c.state.Preferences.Config
	if got.Role != "battery" || got.Mode != "CV" || got.Voltage != 11.1 || c.conn != nil {
		t.Fatal(got)
	}
}

func TestStopOutputReidentifiesSameInstrumentAfterModeReset(t *testing.T) {
	c, f := setup(t)
	c.conn.Close()
	c.conn = nil
	c.reader = nil
	c.state.Unknown = true
	c.begin()
	if e := c.stopOutput(); e != nil {
		t.Fatal(e)
	}
	if !c.state.Connected || c.state.Unknown || !has(f, "OUTP OFF") {
		t.Fatal("OFF recovery failed")
	}
}

func TestStopOutputDoesNotWriteToDifferentInstrument(t *testing.T) {
	c, _ := setup(t)
	c.conn.Close()
	c.conn = nil
	c.reader = nil
	c.state.Unknown = true
	other := fake()
	other.identity = "Keysight Technologies,E36731A,DIFFERENT,1"
	c.dial = func(_, _ string, _ time.Duration) (net.Conn, error) {
		a, b := net.Pipe()
		go other.serve(b)
		return a, nil
	}
	c.begin()
	if c.stopOutput() == nil {
		t.Fatal("different instrument accepted")
	}
	if has(other, "OUTP OFF") || c.conn != nil || !c.state.Unknown {
		t.Fatal("unidentified instrument received control")
	}
}
