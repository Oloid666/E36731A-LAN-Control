package main

import (
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

func TestModeResetRequiresNewSession(t *testing.T) {
	c, f := setup(t)
	f.mu.Lock()
	f.resetSession = true
	f.mu.Unlock()
	c.begin()
	if err := c.apply(batteryConfig()); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	joined := strings.Join(f.commands, "\n")
	if !strings.Contains(joined, "EMUL PS2Q\n*IDN?\nEMUL?\nOUTP?\nSYST:ERR?") {
		t.Fatalf("missing read-only reconnect verification: %s", joined)
	}
	if strings.Count(joined, "EMUL PS2Q") != 1 || strings.Contains(joined, "OUTP ON") {
		t.Fatal("mode switch repeated or output enabled")
	}
}

func TestModeReconnectIdentityMismatchNoWrites(t *testing.T) {
	c, _ := setup(t)
	other := fake()
	other.identity = "Other,E36731A,DIFFERENT,1.0"
	c.dial = func(_, _ string, _ time.Duration) (net.Conn, error) {
		client, server := net.Pipe()
		go other.serve(server)
		return client, nil
	}
	c.begin()
	if err := c.apply(batteryConfig()); err == nil || !strings.Contains(err.Error(), "身分不同") {
		t.Fatalf("unexpected error: %v", err)
	}
	c.fail(errors.New("identity mismatch"))
	other.mu.Lock()
	defer other.mu.Unlock()
	if strings.Join(other.commands, ",") != "*IDN?" || c.state.Config != nil || !c.state.Unknown {
		t.Fatal("unverified instrument received commands or state was armed")
	}
}

func TestModeReconnectFailureDoesNotContinue(t *testing.T) {
	c, f := setup(t)
	c.dial = func(_, _ string, _ time.Duration) (net.Conn, error) {
		return nil, errors.New("reset not ready")
	}
	c.begin()
	c.deadline = time.Now().Add(650 * time.Millisecond)
	if err := c.apply(batteryConfig()); err == nil || !strings.Contains(err.Error(), "未確認成功") {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.conn != nil || c.state.Config != nil || !c.state.Unknown || has(f, "VOLT 3.7") || has(f, "OUTP ON") {
		t.Fatal("failed reconnect continued configuration")
	}
}

func TestModeCancellationDoesNotReconnectOrConfigure(t *testing.T) {
	c, f := setup(t)
	c.dial = func(_, _ string, _ time.Duration) (net.Conn, error) {
		t.Error("cancelled transition reconnected")
		return nil, errors.New("unexpected dial")
	}
	c.begin()
	go func() { time.Sleep(50 * time.Millisecond); c.generation.Add(1) }()
	if err := c.apply(batteryConfig()); err == nil || !strings.Contains(err.Error(), "取消") {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.state.Config != nil || has(f, "OUTP ON") || has(f, "VOLT 3.7") {
		t.Fatal("cancelled transition armed")
	}
}
