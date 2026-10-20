package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeInstrument struct {
	done            chan struct{}
	doneOnce        sync.Once
	mu              sync.Mutex
	commands        []string
	values          map[string]string
	mode            string
	identity        string
	errors          []string
	badReadback     bool
	timeout         string
	input           float64
	errorOn         map[string]string
	rejectPS2Q      bool
	resetSession    bool
	measuredCurrent *float64
	gateCommand     string
	gateEntered     chan struct{}
	gateRelease     <-chan struct{}
	gateOnce        sync.Once
}

func fake() *fakeInstrument {
	return &fakeInstrument{done: make(chan struct{}), mode: "PSUP", identity: "Keysight Technologies,E36731A,MY00001,1.0", input: 5,
		values: map[string]string{"OUTP": "0", "VOLT": "0", "CURR": "0.1", "VOLT:PROT": "4.3", "VOLT:PROT:STAT": "1", "VOLT:PROT:TRIP": "0", "VOLT:MODE": "FIX", "CURR:MODE": "FIX", "OUTP:PMOD": "VOLT", "OUTP:DEL:RISE": "0", "OUTP:DEL:FALL": "0", "FUNC": "CURR", "CURR:LIM": "1", "RES": "4000", "RES:MODE": "FIX"}}
}
func (f *fakeInstrument) serve(conn net.Conn) {
	defer f.doneOnce.Do(func() { close(f.done) })
	defer conn.Close()
	scanner := bufio.NewScanner(conn)
	transitioned := false
	for scanner.Scan() {
		cmd := scanner.Text()
		f.mu.Lock()
		f.commands = append(f.commands, cmd)
		if cmd == f.gateCommand && f.gateRelease != nil {
			release := f.gateRelease
			f.gateOnce.Do(func() { close(f.gateEntered) })
			f.mu.Unlock()
			<-release
			f.mu.Lock()
		}
		if transitioned && f.resetSession {
			f.mu.Unlock()
			continue
		}
		answer := ""
		query := strings.Contains(cmd, "?")
		if cmd == f.timeout {
			f.mu.Unlock()
			continue
		}
		if query {
			switch cmd {
			case "*IDN?":
				answer = f.identity
			case "*OPC?":
				answer = "1"
			case "EMUL?":
				answer = f.mode
			case "SYST:ERR?":
				answer = "0,\"No error\""
				if len(f.errors) > 0 {
					answer = f.errors[0]
					f.errors = f.errors[1:]
				}
			case "STAT:OPER:COND?":
				answer = "1"
				if f.values["OUTP"] == "0" {
					answer = "16"
				} else if f.mode == "LOAD" && f.values["FUNC"] == "CURR" {
					answer = "2"
				} else if f.mode == "LOAD" && f.values["FUNC"] == "RES" {
					answer = "4"
				}
			case "STAT:QUES:COND?":
				answer = "0"
			case "MEAS:VOLT?":
				answer = "0"
				if f.mode == "LOAD" {
					answer = fmt.Sprint(f.input)
				} else if f.values["OUTP"] == "1" {
					answer = f.values["VOLT"]
				}
			case "MEAS:CURR?":
				answer = "0"
				if f.values["OUTP"] == "1" {
					answer = "0.1"
					if f.measuredCurrent != nil {
						answer = fmt.Sprint(*f.measuredCurrent)
					}
				}
			case "MEAS:POW?":
				answer = "0"
				if f.values["OUTP"] == "1" {
					answer = "0.5"
				}
			default:
				answer = f.values[strings.TrimSuffix(cmd, "?")]
				if cmd == "VOLT?" && f.badReadback {
					answer = "29"
				}
			}
		} else {
			parts := strings.SplitN(cmd, " ", 2)
			if len(parts) == 2 {
				key, value := parts[0], parts[1]
				if key == "EMUL" {
					transitioned = true
					if value == "PS2Q" && f.rejectPS2Q {
						f.errors = append(f.errors, "+305,\"Requested operation is unsupported in current mode\"")
					} else {
						f.mode = value
					}
					f.values["OUTP"] = "0"
				}
				if key == "OUTP" {
					if value == "ON" {
						value = "1"
					} else {
						value = "0"
					}
				}
				if key == "VOLT:PROT:STAT" {
					if value == "ON" {
						value = "1"
					} else {
						value = "0"
					}
				}
				f.values[key] = value
				if strings.HasPrefix(cmd, "RES:RANG ") && strings.Contains(cmd, ";:RES ") {
					f.values["RES"] = strings.SplitN(cmd, ";:RES ", 2)[1]
				}
			}
		}
		if errorText, ok := f.errorOn[cmd]; ok {
			f.errors = append(f.errors, errorText)
		}
		f.mu.Unlock()
		if query {
			if _, e := fmt.Fprintln(conn, answer); e != nil {
				return
			}
		}
	}
}
func setup(t *testing.T) (*Controller, *fakeInstrument) {
	t.Helper()
	f := fake()
	c := NewController()
	c.dial = func(_, _ string, _ time.Duration) (net.Conn, error) {
		client, server := net.Pipe()
		go f.serve(server)
		return client, nil
	}
	c.begin()
	if e := c.connect("192.168.1.100"); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if c.conn != nil {
			c.conn.Close()
		}
	})
	return c, f
}
func config(role, mode string) Config {
	return Config{Role: role, Mode: mode, Voltage: 3.7, Current: 0.1, OVP: 4.3}
}
func has(f *fakeInstrument, cmd string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.commands {
		if c == cmd {
			return true
		}
	}
	return false
}
func TestConnectReadOnly(t *testing.T) {
	_, f := setup(t)
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, cmd := range f.commands {
		if !strings.Contains(cmd, "?") {
			t.Fatalf("connect wrote %s", cmd)
		}
	}
}
func TestIdentityMismatchNoControl(t *testing.T) {
	c := NewController()
	f := fake()
	f.identity = "Keysight,E3631A,SN,1"
	c.dial = func(_, _ string, _ time.Duration) (net.Conn, error) { a, b := net.Pipe(); go f.serve(b); return a, nil }
	c.begin()
	if c.connect("192.168.1.10") == nil {
		t.Fatal("wrong model accepted")
	}
	if c.conn != nil || has(f, "OUTP OFF") {
		t.Fatal("wrong model was controlled")
	}
}
func TestValidateRejectsInvalidInputs(t *testing.T) {
	for _, p := range []Config{{Role: "sink", Mode: "CP"}, {Role: "source", Mode: "CV", Voltage: 30, Current: 20, OVP: 33}, {Role: "sink", Mode: "CC", Current: 0.001}, {Role: "source", Mode: "CC", Voltage: 5, Current: .1, OVP: 4}} {
		if p.validate() == nil {
			t.Fatalf("accepted %+v", p)
		}
	}
}
func TestIPAddressPolicy(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "https://192.168.1.1", "8.8.8.8", "::1", "192.168.1.1\nOUTP ON", "224.0.0.1"} {
		if validIP(ip) == nil {
			t.Fatal(ip)
		}
	}
	for _, ip := range []string{"192.168.1.1", "10.2.3.4", "169.254.3.4"} {
		if validIP(ip) != nil {
			t.Fatal(ip)
		}
	}
}
func TestSourceCVApplyThenOnOff(t *testing.T) {
	c, f := setup(t)
	p := config("source", "CV")
	if e := c.apply(p); e != nil {
		t.Fatal(e)
	}
	if has(f, "OUTP ON") {
		t.Fatal("apply enabled output")
	}
	if e := c.on(p); e != nil {
		t.Fatal(e)
	}
	if !c.state.Reading.Output {
		t.Fatal("ON unconfirmed")
	}
	if e := c.off(); e != nil {
		t.Fatal(e)
	}
	if c.state.Config == nil || c.state.Reading.Output {
		t.Fatal("off state invalid")
	}
}
func TestSourceCCIsPreferenceNotForcedMode(t *testing.T) {
	c, f := setup(t)
	p := config("source", "CC")
	if e := c.apply(p); e != nil {
		t.Fatal(e)
	}
	if !has(f, "OUTP:PMOD CURR") || has(f, "FUNC CURR") {
		t.Fatal("source CC must not use load FUNC")
	}
	if e := c.on(p); e != nil {
		t.Fatal(e)
	}
	if c.state.Reading.Actual != "CV" {
		t.Fatal("actual mode must come from status register, not UI selection")
	}
}
func TestSinkCCIgnoresVoltageSetpoint(t *testing.T) {
	c, f := setup(t)
	p := config("sink", "CC")
	if e := c.apply(p); e != nil {
		t.Fatal(e)
	}
	if !has(f, "EMUL LOAD") || !has(f, "FUNC CURR") || !has(f, "CURR 0.1") {
		t.Fatal("CC commands missing")
	}
	if has(f, "VOLT 3.7") || has(f, "VOLT:PROT 4.3") {
		t.Fatal("sink CC must not write voltage or PSU OVP")
	}
	if e := c.on(p); e != nil {
		t.Fatal(e)
	}
	if c.state.Reading.Actual != "CC" {
		t.Fatal("actual mode wrong")
	}
}
func TestSinkCVUsesCurrentLimit(t *testing.T) {
	c, f := setup(t)
	p := config("sink", "CV")
	if e := c.apply(p); e != nil {
		t.Fatal(e)
	}
	if !has(f, "FUNC VOLT") || !has(f, "CURR:LIM 0.1") || !has(f, "VOLT 3.7") {
		t.Fatal("CV command mapping wrong")
	}
	if e := c.on(p); e != nil {
		t.Fatal(e)
	}
}
func TestReadbackMismatchBlocksOn(t *testing.T) {
	c, f := setup(t)
	f.mu.Lock()
	f.badReadback = true
	f.mu.Unlock()
	if c.apply(config("source", "CV")) == nil {
		t.Fatal("readback mismatch ignored")
	}
	if c.state.Config != nil || has(f, "OUTP ON") {
		t.Fatal("unverified settings accepted")
	}
}
func TestChangedConfigurationBlocksOn(t *testing.T) {
	c, f := setup(t)
	p := config("source", "CV")
	if e := c.apply(p); e != nil {
		t.Fatal(e)
	}
	f.mu.Lock()
	f.values["CURR"] = "20"
	f.mu.Unlock()
	if c.on(p) == nil || has(f, "OUTP ON") {
		t.Fatal("external setting change ignored")
	}
}
func TestChangedUIBlocksOn(t *testing.T) {
	c, f := setup(t)
	p := config("source", "CV")
	if e := c.apply(p); e != nil {
		t.Fatal(e)
	}
	p.Current = .2
	if c.on(p) == nil || has(f, "OUTP ON") {
		t.Fatal("new UI config accepted without apply")
	}
}
func TestOnWithoutApplyRejected(t *testing.T) {
	c, f := setup(t)
	if c.on(config("sink", "CC")) == nil || has(f, "OUTP ON") {
		t.Fatal("ON without apply")
	}
}
func TestSCPIErrorNotSilenced(t *testing.T) {
	c, f := setup(t)
	f.mu.Lock()
	f.errors = []string{"-222,\"Data out of range\""}
	f.mu.Unlock()
	if e := c.apply(config("source", "CV")); e == nil || !strings.Contains(e.Error(), "-222") {
		t.Fatal(e)
	}
	if has(f, "*CLS") || has(f, "OUTP ON") {
		t.Fatal("SCPI error silenced")
	}
}
func TestOVPTripBlocksOn(t *testing.T) {
	c, f := setup(t)
	p := config("source", "CV")
	if e := c.apply(p); e != nil {
		t.Fatal(e)
	}
	f.mu.Lock()
	f.values["VOLT:PROT:TRIP"] = "1"
	f.mu.Unlock()
	if c.on(p) == nil || has(f, "OUTP ON") {
		t.Fatal("OVP trip ignored")
	}
}
func TestOffCancelsEnableBeforeSend(t *testing.T) {
	c, f := setup(t)
	p := config("source", "CV")
	if e := c.apply(p); e != nil {
		t.Fatal(e)
	}
	c.generation.Add(1)
	if c.on(p) == nil || has(f, "OUTP ON") {
		t.Fatal("cancel ignored")
	}
	c.begin()
	if e := c.off(); e != nil {
		t.Fatal(e)
	}
}
func TestTimeoutClearsReadingsAndDoesNotConfirmOff(t *testing.T) {
	c, f := setup(t)
	f.mu.Lock()
	f.timeout = "MEAS:VOLT?"
	f.mu.Unlock()
	c.deadline = time.Now().Add(20 * time.Millisecond)
	e := c.sample()
	if e == nil {
		t.Fatal("timeout ignored")
	}
	c.fail(e)
	<-f.done
	if c.state.Reading != nil || !c.state.Unknown || c.state.Connected {
		t.Fatal("stale readings or false OFF")
	}
	if !has(f, "OUTP OFF") {
		t.Fatal("no best-effort OFF")
	}
}
func TestSinkPrecheckPowerLimit(t *testing.T) {
	c, f := setup(t)
	p := config("sink", "CC")
	p.Current = 20
	if e := c.apply(p); e != nil {
		t.Fatal(e)
	}
	f.mu.Lock()
	f.input = 15
	f.mu.Unlock()
	if c.on(p) == nil || has(f, "OUTP ON") {
		t.Fatal("300W load accepted")
	}
}
func TestHTTPAccessAndValidation(t *testing.T) {
	c, f := setup(t)
	s := &Server{controller: c, token: "session", host: "127.0.0.1:1234", quit: make(chan struct{}, 1)}
	for _, test := range []struct {
		path, method, token, origin, body string
		code                              int
	}{{"/api/state", "GET", "", "", "", 403}, {"/api/state", "GET", "session", "http://evil.test", "", 403}, {"/api/action", "POST", "session", "http://127.0.0.1:1234", `{"action":"connect","ip":"127.0.0.1"}`, 400}, {"/api/action", "POST", "session", "", `{"action":"raw","command":"OUTP ON"}`, 400}, {"/api/state", "GET", "session", "", "", 200}, {"/api/heartbeat", "GET", "session", "", "", 204}} {
		t.Run(test.path+test.token+test.origin+test.body, func(t *testing.T) {
			r := httptest.NewRequest(test.method, "http://127.0.0.1:1234"+test.path, strings.NewReader(test.body))
			r.Header.Set("X-Control-Token", test.token)
			r.Header.Set("Origin", test.origin)
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			s.ServeHTTP(w, r)
			if w.Code != test.code {
				t.Fatalf("%d: %s", w.Code, w.Body.String())
			}
		})
	}
	if has(f, "OUTP ON") {
		t.Fatal("HTTP validation wrote ON")
	}
}
func TestHTTPInvalidConfigPreservesConnection(t *testing.T) {
	c, _ := setup(t)
	s := &Server{controller: c, token: "s", host: "localhost", quit: make(chan struct{}, 1)}
	r := httptest.NewRequest("POST", "http://localhost/api/action", strings.NewReader(`{"action":"apply","config":{"role":"source","mode":"CV","voltage":30,"current":20,"ovp":33}}`))
	r.Header.Set("X-Control-Token", "s")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 400 || !c.state.Connected {
		t.Fatal(w.Code, c.state)
	}
}
func TestStateJSONHasNoSessionSecret(t *testing.T) {
	c, _ := setup(t)
	s := &Server{controller: c, token: "do-not-expose", host: "localhost"}
	r := httptest.NewRequest("GET", "http://localhost/api/state", nil)
	r.Header.Set("X-Control-Token", s.token)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	var state State
	if json.Unmarshal(w.Body.Bytes(), &state) != nil || !state.Connected {
		t.Fatal(w.Body.String())
	}
	if strings.Contains(w.Body.String(), s.token) {
		t.Fatal("secret exposed")
	}
}

// Regression: this firmware can identify/read V and I but never replies to POW.
func TestPowerComputedWithoutPowerQuery(t *testing.T) {
	c, f := setup(t)
	f.mu.Lock()
	f.timeout = "MEAS:POW?"
	f.values["OUTP"] = "1"
	f.values["VOLT"] = "8"
	f.mu.Unlock()
	c.begin()
	c.deadline = time.Now().Add(100 * time.Millisecond)
	if e := c.sample(); e != nil {
		t.Fatal(e)
	}
	if c.state.Reading == nil || c.state.Reading.Power != 0.8 {
		t.Fatalf("incorrect calculated power: %+v", c.state.Reading)
	}
	if has(f, "MEAS:POW?") {
		t.Fatal("power query reintroduced")
	}
}

func TestLoadResistanceIsReadWithoutActivatingOutput(t *testing.T) {
	c, f := setup(t)
	f.mu.Lock()
	f.mode = "LOAD"
	f.values["RES"] = "12.5"
	f.mu.Unlock()
	if e := c.sample(); e != nil {
		t.Fatal(e)
	}
	if c.state.Reading.Resistance == nil || *c.state.Reading.Resistance != 12.5 {
		t.Fatal("RES setpoint missing")
	}
	if has(f, "OUTP ON") {
		t.Fatal("reading resistance enabled output")
	}
}
func TestSinkCRApplyReadbackAndOn(t *testing.T) {
	c, f := setup(t)
	p := config("sink", "CR")
	p.Resistance = 100
	if e := c.apply(p); e != nil {
		t.Fatal(e)
	}
	if !has(f, "FUNC RES") || !has(f, "RES:MODE FIX") || !has(f, "RES:RANG 1250;:RES 100") {
		t.Fatal("CR command sequence wrong")
	}
	if has(f, "OUTP ON") {
		t.Fatal("apply enabled output")
	}
	if e := c.on(p); e != nil {
		t.Fatal(e)
	}
	if c.state.Reading.Actual != "CR" || *c.state.Reading.Resistance != 100 {
		t.Fatal("CR readback wrong")
	}
}
func TestSinkCRRangesAndInvalidResistance(t *testing.T) {
	for _, resistance := range []float64{0, 0.079, 4000.1} {
		p := config("sink", "CR")
		p.Resistance = resistance
		if p.validate() == nil {
			t.Fatal("invalid R accepted")
		}
	}
	for _, test := range []struct {
		r   float64
		cmd string
	}{{.08, "RES:RANG 30;:RES 0.08"}, {10, "RES:RANG 30;:RES 10"}, {1250, "RES:RANG 1250;:RES 1250"}, {4000, "RES:RANG 4000;:RES 4000"}} {
		c, f := setup(t)
		p := config("sink", "CR")
		p.Resistance = test.r
		if e := c.apply(p); e != nil {
			t.Fatal(e)
		}
		if !has(f, test.cmd) {
			t.Fatal(test.cmd)
		}
	}
}
func TestSinkCRRefusesPredictedOvercurrent(t *testing.T) {
	c, f := setup(t)
	p := config("sink", "CR")
	p.Resistance = .1
	if e := c.apply(p); e != nil {
		t.Fatal(e)
	}
	if c.on(p) == nil || has(f, "OUTP ON") {
		t.Fatal("predicted 50 A accepted")
	}
}
func TestSourceCannotUseLoadCR(t *testing.T) {
	p := config("source", "CR")
	p.Resistance = 100
	if p.validate() == nil {
		t.Fatal("source accepted load CR mode")
	}
}
func TestChangedResistanceBlocksOn(t *testing.T) {
	c, f := setup(t)
	p := config("sink", "CR")
	p.Resistance = 100
	if e := c.apply(p); e != nil {
		t.Fatal(e)
	}
	f.mu.Lock()
	f.values["RES"] = "0.1"
	f.mu.Unlock()
	if c.on(p) == nil || has(f, "OUTP ON") {
		t.Fatal("external R change ignored")
	}
}
