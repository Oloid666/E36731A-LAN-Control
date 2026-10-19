package main

import (
	"errors"
	"fmt"
	"math"
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
func (c *Controller) verifyESR(p Config) error {
	// Public guide documents PS2Q and VOLT/CURR/OVP, not an ESR register.
	// This verifies a host-controlled voltage setpoint, never a native ESR setting.
	for _, pair := range []struct {
		q     string
		value float64
	}{{"VOLT?", c.esrCommand}, {"CURR?", p.Current}, {"VOLT:PROT?", p.OVP}} {
		if e := c.compare(pair.q, pair.value); e != nil {
			return e
		}
	}
	enabled, e := c.flag("VOLT:PROT:STAT?")
	if e != nil {
		return e
	}
	if !enabled {
		return errors.New("ESR 模式要求 OVP 啟用")
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
func esrTarget(p Config, current float64) (float64, error) {
	if math.IsNaN(current) || math.IsInf(current, 0) {
		return 0, errors.New("ESR 電流讀值無效")
	}
	if math.Abs(current) > p.Current+0.002 {
		return 0, errors.New("ESR 超過設定的雙向軟體電流監控值")
	}
	target := p.Voltage - current*p.ESRMilliohm/1000
	if target < p.MinVoltage || target > p.MaxVoltage {
		return 0, fmt.Errorf("ESR 計算端電壓 %.6g V 超出設定範圍，停止而非截斷模型", target)
	}
	return target, nil
}
func esrNext(previous, target float64) float64 {
	// A maximum 5 mV change per poll limits sudden software-induced steps.
	delta := target - previous
	if delta > .005 {
		delta = .005
	}
	if delta < -.005 {
		delta = -.005
	}
	return previous + delta
}
func (c *Controller) feedbackESR(r *Reading) error {
	p := *c.state.Config
	if r.Emulation != "PS2Q" {
		return errors.New("ESR 需要 PS2Q 模式")
	}
	if r.Voltage < p.MinVoltage-.01 || r.Voltage > p.MaxVoltage+.01 {
		return errors.New("ESR 實測電壓超出允許範圍")
	}
	// Detect another controller/front-panel changing the voltage/current limits.
	if e := c.verifyESR(p); e != nil {
		return e
	}
	target, e := esrTarget(p, r.Current)
	if e != nil {
		return e
	}
	next := esrNext(c.esrCommand, target)
	if math.Abs(next-c.esrCommand) >= 0.00065 {
		if e = c.write(fmt.Sprintf("VOLT %.9g", next)); e != nil {
			return e
		}
		if e = c.opc(); e != nil {
			return e
		}
		if e = c.errors(); e != nil {
			return e
		}
		if e = c.compare("VOLT?", next); e != nil {
			return e
		}
		c.esrCommand = next
		c.log(fmt.Sprintf("SOFTWARE ESR %.6g mOhm: I=%.6g A target=%.6g V command=%.6g V", p.ESRMilliohm, r.Current, target, next))
	}
	command := c.esrCommand
	r.ESRTarget = &target
	r.ESRCommand = &command
	return nil
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
