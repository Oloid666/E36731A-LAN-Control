package main

import (
	"errors"
	"fmt"
	"time"
)

type ConnectionOptions struct {
	TimeoutMS int `json:"timeout_ms"`
	PollMS    int `json:"poll_ms"`
}

func DefaultOptions() ConnectionOptions { return ConnectionOptions{TimeoutMS: 5000, PollMS: 1000} }
func (o ConnectionOptions) normalized() (ConnectionOptions, error) {
	if o.TimeoutMS == 0 {
		o.TimeoutMS = 5000
	}
	if o.PollMS == 0 {
		o.PollMS = 1000
	}
	if o.TimeoutMS < 1000 || o.TimeoutMS > 10000 || o.PollMS < 1000 || o.PollMS > 5000 {
		return o, errors.New("通訊逾時範圍 1000–10000 ms；輪詢間隔範圍 1000–5000 ms")
	}
	return o, nil
}
func (c *Controller) verifyPS2Q(p Config) error {
	// Verify only documented fixed PS2Q voltage/current/OVP settings.
	// No native ESR command is documented or sent.
	for _, pair := range []struct {
		q     string
		value float64
	}{{"VOLT?", p.Voltage}, {"CURR?", p.Current}, {"VOLT:PROT?", p.OVP}} {
		if e := c.compare(pair.q, pair.value); e != nil {
			return e
		}
	}
	enabled, e := c.flag("VOLT:PROT:STAT?")
	if e != nil {
		return e
	}
	if !enabled {
		return errors.New("PS2Q 模式要求 OVP 啟用")
	}
	tripped, e := c.flag("VOLT:PROT:TRIP?")
	if e != nil {
		return e
	}
	if tripped {
		return errors.New("OVP 已觸發")
	}
	return c.errors()
}

// Monitoring never adjusts voltage based on measured current.
func (c *Controller) monitorPS2Q(r *Reading) error {
	p := *c.state.Config
	if r.Voltage < p.MinVoltage-.01 || r.Voltage > p.MaxVoltage+.01 {
		return errors.New("PS2Q 實測電壓超出允許範圍")
	}
	if r.Current > p.Current+.002 || r.Current < -p.Current-.002 {
		return errors.New("PS2Q 超過雙向軟體電流監控值；此監控不是硬體 Sink 限流")
	}
	return c.verifyPS2Q(p)
}

type DiagnosticItem struct {
	Command    string  `json:"command"`
	Response   string  `json:"response"`
	DurationMS float64 `json:"duration_ms"`
	Error      string  `json:"error,omitempty"`
}

func (c *Controller) diagnose() error {
	on, e := c.flag("OUTP?")
	if e != nil {
		return e
	}
	if on {
		return errors.New("唯讀診斷請先關閉輸出")
	}
	if e = c.errors(); e != nil {
		return e
	}
	c.state.Diagnostics = nil
	mode, e := c.query("EMUL?")
	if e != nil {
		return e
	}
	queries := []string{"*IDN?", "EMUL?", "OUTP?", "MEAS:VOLT?", "MEAS:CURR?", "STAT:OPER:COND?", "STAT:QUES:COND?"}
	if mode == "LOAD" {
		queries = append(queries, "FUNC?", "RES?")
	} else if mode == "PSUP" || mode == "PS2Q" {
		queries = append(queries, "VOLT?", "CURR?", "VOLT:PROT?", "VOLT:PROT:STAT?")
	}
	for _, q := range queries {
		started := time.Now()
		response, err := c.query(q)
		item := DiagnosticItem{Command: q, Response: response, DurationMS: float64(time.Since(started).Microseconds()) / 1000}
		if err != nil {
			item.Error = err.Error()
			c.state.Diagnostics = append(c.state.Diagnostics, item)
			return err
		}
		found, err := c.readErrors()
		if err != nil {
			item.Error = err.Error()
			c.state.Diagnostics = append(c.state.Diagnostics, item)
			return err
		}
		if len(found) > 0 {
			item.Error = fmt.Sprint(found)
		}
		c.state.Diagnostics = append(c.state.Diagnostics, item)
	}
	c.state.Message = "唯讀診斷完成，結果可匯出；未變更參數或開啟輸出"
	return nil
}
