package main

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestPS2QDoesNotRequireOPCOrFrontPanelRemote(t *testing.T) {
	c, f := setup(t)
	f.mu.Lock()
	f.timeout = "*OPC?"
	f.errorOn = map[string]string{"SYST:REM": `+305,"unsupported"`, "VOLT:PROT:STAT ON": `+305,"unsupported"`}
	f.mu.Unlock()
	if e := c.apply(batteryConfig()); e != nil {
		t.Fatal(e)
	}
	for _, cmd := range []string{"*OPC?", "SYST:REM", "VOLT:PROT:STAT ON", "OUTP ON"} {
		if has(f, cmd) {
			t.Fatal("unnecessary or unsafe command", cmd)
		}
	}
	if c.state.Config == nil || c.state.Reading.Output {
		t.Fatal("did not verify OFF configuration")
	}
}

func TestPS2QReportsFirstUnsupportedCommandAndStops(t *testing.T) {
	for _, cmd := range []string{"VOLT:PROT 4.3", "CURR 1", "VOLT 3.7", "VOLT:PROT:STAT?", "VOLT:PROT:TRIP?"} {
		t.Run(cmd, func(t *testing.T) {
			c, f := setup(t)
			f.mu.Lock()
			f.mode = "PS2Q"
			f.errorOn = map[string]string{cmd: `+305,"Requested operation is unsupported in current mode"`}
			f.mu.Unlock()
			e := c.apply(batteryConfig())
			var ce *commandError
			if !errors.As(e, &ce) || ce.Command != cmd || !strings.Contains(e.Error(), "305") {
				t.Fatalf("wrong attribution: %v", e)
			}
			if cmd == "VOLT:PROT 4.3" && (has(f, "CURR 1") || has(f, "VOLT 3.7")) {
				t.Fatal("continued after OVP failure")
			}
			if cmd == "CURR 1" && has(f, "VOLT 3.7") {
				t.Fatal("continued after current failure")
			}
			if c.state.Config != nil || has(f, "OUTP ON") || has(f, "*OPC?") {
				t.Fatal("unsupported configuration armed")
			}
			c.fail(e)
			if !strings.HasPrefix(c.state.LastFailure, cmd+" →") || c.state.Unknown || c.conn != nil {
				t.Fatalf("failed command or OFF confirmation lost: %+v", c.state)
			}
		})
	}
}

func TestPS2QEnablesDisabledOVPAndRejectsUnsupportedEnable(t *testing.T) {
	for _, reject := range []bool{false, true} {
		c, f := setup(t)
		f.mu.Lock()
		f.mode = "PS2Q"
		f.values["VOLT:PROT:STAT"] = "0"
		if reject {
			f.errorOn = map[string]string{"VOLT:PROT:STAT ON": `+305,"unsupported"`}
		}
		f.mu.Unlock()
		e := c.apply(batteryConfig())
		if !has(f, "VOLT:PROT:STAT ON") {
			t.Fatal("disabled protection not enabled")
		}
		if reject {
			if e == nil || !strings.HasPrefix(e.Error(), "VOLT:PROT:STAT ON →") || c.state.Config != nil || has(f, "CURR 1") {
				t.Fatal("rejected enable bypassed", e)
			}
		} else if e != nil || c.state.Config == nil {
			t.Fatal("OVP not verified", e)
		}
	}
}

func TestPS2QRetriesOnlyCleanReadbackMismatch(t *testing.T) {
	c, f := setup(t)
	f.mu.Lock()
	f.mode = "PS2Q"
	f.readbacks = map[string][]string{"VOLT?": {"3.6", "3.7"}}
	f.mu.Unlock()
	if e := c.apply(batteryConfig()); e != nil {
		t.Fatal(e)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	writes := 0
	for _, cmd := range f.commands {
		if cmd == "VOLT?" {
			count++
		}
		if cmd == "VOLT 3.7" {
			writes++
		}
	}
	if count < 2 || writes != 1 || c.state.Config == nil {
		t.Fatal("readback retry rewrote settings or failed")
	}
}

func TestPS2QReadbackDeadlineDoesNotArm(t *testing.T) {
	c, f := setup(t)
	f.mu.Lock()
	f.mode = "PS2Q"
	f.badReadback = true
	f.mu.Unlock()
	c.deadline = time.Now().Add(250 * time.Millisecond)
	if e := c.apply(batteryConfig()); e == nil {
		t.Fatal("mismatched readback accepted")
	}
	if c.state.Config != nil || has(f, "OUTP ON") {
		t.Fatal("mismatched configuration armed")
	}
}

func TestPS2QRejectsOVPTripAndInvalidFlag(t *testing.T) {
	for _, flag := range []string{"VOLT:PROT:TRIP", "VOLT:PROT:STAT"} {
		c, f := setup(t)
		f.mu.Lock()
		f.mode = "PS2Q"
		if flag == "VOLT:PROT:TRIP" {
			f.values[flag] = "1"
		} else {
			f.values[flag] = "2"
		}
		f.mu.Unlock()
		if e := c.apply(batteryConfig()); e == nil {
			t.Fatal("invalid protection accepted", flag)
		}
		if c.state.Config != nil || has(f, "OUTP ON") {
			t.Fatal("invalid protection armed")
		}
	}
}

func TestDisconnectRecoversUnknownOutputBeforeClosing(t *testing.T) {
	c, f := setup(t)
	c.conn.Close()
	c.conn = nil
	c.reader = nil
	c.state.Unknown = true
	c.begin()
	if e := c.disconnect(); e != nil {
		t.Fatal(e)
	}
	if c.conn != nil || c.state.Connected || c.state.Unknown || c.state.Config != nil || !has(f, "OUTP OFF") {
		t.Fatal("disconnect failed OFF recovery")
	}
}
