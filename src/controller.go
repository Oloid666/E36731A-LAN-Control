package main

import (
	"bufio"
	"errors"
	"fmt"
	"math"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Config struct {
	Role    string  `json:"role"`
	Mode    string  `json:"mode"`
	Voltage float64 `json:"voltage"`
	Current float64 `json:"current"`
	OVP     float64 `json:"ovp"`
}
type Reading struct {
	Time         string  `json:"time"`
	Voltage      float64 `json:"voltage"`
	Current      float64 `json:"current"`
	Power        float64 `json:"power"`
	Output       bool    `json:"output"`
	Actual       string  `json:"actual"`
	Emulation    string  `json:"emulation"`
	Questionable int     `json:"questionable"`
}
type State struct {
	Connected bool      `json:"connected"`
	Identity  string    `json:"identity"`
	IP        string    `json:"ip"`
	Config    *Config   `json:"config"`
	Reading   *Reading  `json:"reading"`
	History   []Reading `json:"history"`
	Logs      []string  `json:"logs"`
	Message   string    `json:"message"`
	Error     string    `json:"error"`
	Unknown   bool      `json:"unknown"`
}
type Controller struct {
	mu         sync.Mutex
	conn       net.Conn
	reader     *bufio.Reader
	dial       func(string, string, time.Duration) (net.Conn, error)
	state      State
	generation atomic.Int64
	ticket     int64
	deadline   time.Time
	heartbeat  atomic.Int64
	busy       atomic.Bool
}

func NewController() *Controller {
	c := &Controller{dial: net.DialTimeout}
	c.state.Message = "未連線；實機驗證尚未完成"
	c.heartbeat.Store(time.Now().UnixNano())
	return c
}
func (c *Controller) log(s string) {
	c.state.Logs = append(c.state.Logs, time.Now().Format("15:04:05 ")+s)
	if len(c.state.Logs) > 120 {
		c.state.Logs = c.state.Logs[len(c.state.Logs)-120:]
	}
}
func validIP(value string) error {
	ip := net.ParseIP(value)
	if ip == nil || ip.To4() == nil || !(ip.IsPrivate() || ip.IsLinkLocalUnicast()) {
		return errors.New("請輸入儀器的私人或 link-local IPv4 位址，例如 192.168.1.100；不能貼網址")
	}
	return nil
}
func (p Config) validate() error {
	if p.Role != "source" && p.Role != "sink" {
		return errors.New("Role 必須為 source 或 sink")
	}
	if p.Mode != "CV" && p.Mode != "CC" {
		return errors.New("Mode 必須為 CC 或 CV")
	}
	for _, v := range []float64{p.Voltage, p.Current, p.OVP} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return errors.New("參數必須為有限數值")
		}
	}
	if p.Current <= 0 || p.Current > 20 {
		return errors.New("本版本電流範圍 >0–20 A")
	}
	if p.Role == "source" {
		if p.Voltage < 0 || p.Voltage > 30 || p.OVP <= p.Voltage || p.OVP > 33 {
			return errors.New("Source：電壓 0–30 V，OVP 高於電壓且不超過 33 V")
		}
	} else {
		if p.Current < 0.01 {
			return errors.New("Sink 高電流檔最小設定為 0.01 A")
		}
		if p.Mode == "CV" && (p.Voltage < 0.02 || p.Voltage > 30) {
			return errors.New("Sink CV：電壓範圍 0.02–30 V")
		}
	}
	if (p.Role == "source" || p.Mode == "CV") && p.Voltage*p.Current > 200 {
		return errors.New("本版本設定 V×I 上限為 200 W")
	}
	return nil
}
func (c *Controller) begin() {
	c.ticket = c.generation.Load()
	c.deadline = time.Now().Add(12 * time.Second)
}
func (c *Controller) ready() error {
	if c.conn == nil {
		return errors.New("尚未連線")
	}
	if c.generation.Load() != c.ticket {
		return errors.New("操作已被 OFF 要求取消")
	}
	if time.Now().After(c.deadline) {
		return errors.New("本次操作逾時")
	}
	d := time.Now().Add(2 * time.Second)
	if c.deadline.Before(d) {
		d = c.deadline
	}
	return c.conn.SetDeadline(d)
}
func (c *Controller) write(cmd string) error {
	if err := c.ready(); err != nil {
		return err
	}
	c.log("TX " + cmd)
	_, err := fmt.Fprint(c.conn, cmd+"\n")
	return err
}
func (c *Controller) query(cmd string) (string, error) {
	if err := c.write(cmd); err != nil {
		return "", err
	}
	// Bound the line so a wrong TCP service cannot allocate arbitrary memory.
	var line []byte
	for len(line) < 4096 {
		b, err := c.reader.ReadByte()
		if err != nil {
			return "", err
		}
		if b == '\n' {
			s := strings.TrimSpace(string(line))
			c.log("RX " + s)
			return s, nil
		}
		line = append(line, b)
	}
	return "", errors.New("SCPI 回覆超過 4096 bytes")
}
func (c *Controller) number(cmd string) (float64, error) {
	s, e := c.query(cmd)
	if e != nil {
		return 0, e
	}
	v, e := strconv.ParseFloat(s, 64)
	if e != nil || math.IsNaN(v) || math.IsInf(v, 0) || math.Abs(v) > 1e30 {
		return 0, fmt.Errorf("%s 回傳無效數值 %q", cmd, s)
	}
	return v, nil
}
func (c *Controller) flag(cmd string) (bool, error) {
	v, e := c.number(cmd)
	if e != nil {
		return false, e
	}
	if v != 0 && v != 1 {
		return false, fmt.Errorf("%s 回傳無效狀態", cmd)
	}
	return v == 1, nil
}
func (c *Controller) bits(cmd string) (int, error) {
	v, e := c.number(cmd)
	if e != nil {
		return 0, e
	}
	if v < 0 || v > 65535 || v != math.Trunc(v) {
		return 0, errors.New("無效狀態暫存器")
	}
	return int(v), nil
}
func (c *Controller) errors() error {
	var list []string
	for i := 0; i < 21; i++ {
		s, e := c.query("SYST:ERR?")
		if e != nil {
			return e
		}
		v, e := strconv.Atoi(strings.TrimSpace(strings.SplitN(s, ",", 2)[0]))
		if e != nil {
			return fmt.Errorf("錯誤佇列格式錯誤：%s", s)
		}
		if v == 0 {
			if len(list) > 0 {
				return errors.New("SCPI：" + strings.Join(list, " / "))
			}
			return nil
		}
		list = append(list, s)
	}
	return errors.New("錯誤佇列未清空：" + strings.Join(list, " / "))
}
func (c *Controller) opc() error {
	s, e := c.query("*OPC?")
	if e != nil {
		return e
	}
	if s != "1" {
		return errors.New("儀器未確認操作完成")
	}
	return nil
}
func (c *Controller) connect(ip string) error {
	if err := validIP(ip); err != nil {
		return err
	}
	if c.conn != nil {
		return errors.New("請先 OFF 並斷線")
	}
	c.state.Config = nil
	c.state.Reading = nil
	c.state.Identity = ""
	c.state.Error = ""
	conn, e := c.dial("tcp", net.JoinHostPort(ip, "5025"), 3*time.Second)
	if e != nil {
		return e
	}
	c.conn = conn
	c.reader = bufio.NewReader(conn)
	identity, e := c.query("*IDN?")
	if e != nil {
		conn.Close()
		c.conn = nil
		return e
	}
	fields := strings.Split(identity, ",")
	if len(fields) < 2 || strings.ToUpper(strings.TrimSpace(fields[1])) != "E36731A" {
		conn.Close()
		c.conn = nil
		return fmt.Errorf("型號不是 E36731A，未傳送控制指令：%s", identity)
	}
	c.state.Connected = true
	c.state.IP = ip
	c.state.Identity = identity
	c.state.Unknown = false
	c.state.History = nil
	c.state.Message = "已連線；未變更設定或開啟輸出"
	c.heartbeat.Store(time.Now().UnixNano())
	return c.sample()
}
func (c *Controller) apply(p Config) error {
	if e := p.validate(); e != nil {
		return e
	}
	c.state.Config = nil
	on, e := c.flag("OUTP?")
	if e != nil {
		return e
	}
	if on {
		return errors.New("請先 OFF 再套用設定")
	}
	if e = c.errors(); e != nil {
		return e
	}
	target := "PSUP"
	if p.Role == "sink" {
		target = "LOAD"
	}
	mode, e := c.query("EMUL?")
	if e != nil {
		return e
	}
	if mode != target {
		if e = c.write("EMUL " + target); e != nil {
			return e
		}
		if e = c.opc(); e != nil {
			return e
		}
		if e = c.errors(); e != nil {
			return e
		}
	}
	commands := []string{"SYST:REM", "INIT:CONT:TRAN OFF", "ABOR:TRAN", "VOLT:MODE FIX", "CURR:MODE FIX", "OUTP:DEL:RISE 0", "OUTP:DEL:FALL 0"}
	if p.Role == "source" {
		preferred := "VOLT"
		if p.Mode == "CC" {
			preferred = "CURR"
		}
		commands = append(commands, "OUTP:PMOD "+preferred, fmt.Sprintf("VOLT:PROT %.9g", p.OVP), "VOLT:PROT:STAT ON", fmt.Sprintf("CURR %.9g", p.Current), fmt.Sprintf("VOLT %.9g", p.Voltage))
	} else {
		// Load RES is input resistance, not battery internal resistance. No RES commands.
		commands = append(commands, "CURR:RANG MAX")
		if p.Mode == "CV" {
			commands = append(commands, "VOLT:RANG MAX", "FUNC VOLT", fmt.Sprintf("CURR:LIM %.9g", p.Current), fmt.Sprintf("VOLT %.9g", p.Voltage))
		} else {
			commands = append(commands, "FUNC CURR", fmt.Sprintf("CURR %.9g", p.Current))
		}
	}
	for _, cmd := range commands {
		if e = c.write(cmd); e != nil {
			return e
		}
	}
	if e = c.opc(); e != nil {
		return e
	}
	if e = c.errors(); e != nil {
		return e
	}
	if e = c.verify(p); e != nil {
		return e
	}
	c.state.Config = &p
	c.state.Error = ""
	c.state.Message = "設定讀回驗證完成；輸出仍為 OFF"
	return c.sample()
}
func (c *Controller) compare(cmd string, want float64) error {
	got, e := c.number(cmd)
	if e != nil {
		return e
	}
	if math.Abs(got-want) > math.Max(0.002, math.Abs(want)*1e-4) {
		return fmt.Errorf("讀回不符：%s = %.6g，預期 %.6g", cmd, got, want)
	}
	return nil
}
func (c *Controller) verify(p Config) error {
	expected := "PSUP"
	if p.Role == "sink" {
		expected = "LOAD"
	}
	s, e := c.query("EMUL?")
	if e != nil {
		return e
	}
	if s != expected {
		return errors.New("操作模式讀回不符")
	}
	if p.Role == "source" {
		for _, v := range []struct {
			q string
			v float64
		}{{"VOLT?", p.Voltage}, {"CURR?", p.Current}, {"VOLT:PROT?", p.OVP}} {
			if e = c.compare(v.q, v.v); e != nil {
				return e
			}
		}
		enabled, e := c.flag("VOLT:PROT:STAT?")
		if e != nil {
			return e
		}
		if !enabled {
			return errors.New("OVP 未啟用")
		}
		trip, e := c.flag("VOLT:PROT:TRIP?")
		if e != nil {
			return e
		}
		if trip {
			return errors.New("OVP 已觸發，請先處理實機原因")
		}
		prefer, e := c.query("OUTP:PMOD?")
		if e != nil {
			return e
		}
		want := "VOLT"
		if p.Mode == "CC" {
			want = "CURR"
		}
		if prefer != want {
			return errors.New("Source 轉換偏好讀回不符")
		}
	} else {
		f, e := c.query("FUNC?")
		if e != nil {
			return e
		}
		if p.Mode == "CV" {
			if f != "VOLT" {
				return errors.New("Sink 不在 CV 模式")
			}
			if e = c.compare("VOLT?", p.Voltage); e != nil {
				return e
			}
			if e = c.compare("CURR:LIM?", p.Current); e != nil {
				return e
			}
		} else {
			if f != "CURR" {
				return errors.New("Sink 不在 CC 模式")
			}
			if e = c.compare("CURR?", p.Current); e != nil {
				return e
			}
		}
	}
	for _, q := range []string{"VOLT:MODE?", "CURR:MODE?"} {
		s, e := c.query(q)
		if e != nil {
			return e
		}
		if s != "FIX" {
			return errors.New("儀器非固定輸出模式")
		}
	}
	for _, q := range []string{"OUTP:DEL:RISE?", "OUTP:DEL:FALL?"} {
		if e = c.compare(q, 0); e != nil {
			return e
		}
	}
	if e = c.errors(); e != nil {
		return e
	}
	return nil
}
func (c *Controller) on(expected Config) error {
	if c.state.Config == nil || *c.state.Config != expected {
		return errors.New("設定尚未驗證或已變更，請先套用")
	}
	if e := c.verify(expected); e != nil {
		return e
	}
	// Sink CC has no commanded voltage. Check externally applied voltage before ON.
	if expected.Role == "sink" {
		v, e := c.number("MEAS:VOLT?")
		if e != nil {
			return e
		}
		if math.Abs(v) > 30 || math.Abs(v)*expected.Current > 200 {
			return errors.New("輸入電壓或預估功率超過本版本 30 V／200 W 限制")
		}
	}
	if e := c.write("OUTP ON"); e != nil {
		return e
	}
	if e := c.errors(); e != nil {
		return e
	}
	enabled, e := c.flag("OUTP?")
	if e != nil {
		return e
	}
	if !enabled {
		return errors.New("儀器未確認 ON，請檢查保護或 inhibit 狀態")
	}
	c.state.Message = "儀器已確認 ON"
	return c.sample()
}
func (c *Controller) off() error {
	c.state.Config = nil
	if c.conn == nil {
		if c.state.Unknown {
			return errors.New("未連線，無法確認 OFF；請檢查實機面板")
		}
		return nil
	}
	if e := c.write("OUTP OFF"); e != nil {
		return e
	}
	enabled, e := c.flag("OUTP?")
	if e != nil {
		return e
	}
	if enabled {
		return errors.New("儀器仍回報 ON")
	}
	c.state.Unknown = false
	c.state.Message = "儀器已確認 OFF；重新 ON 前請套用設定"
	if c.state.Reading != nil {
		c.state.Reading.Output = false
		c.state.Reading.Actual = "OFF"
	}
	return nil
}
func (c *Controller) disconnect() error {
	if c.conn == nil {
		return nil
	}
	if e := c.off(); e != nil {
		return e
	}
	if e := c.write("SYST:LOC"); e != nil {
		return e
	}
	c.conn.Close()
	c.conn = nil
	c.reader = nil
	c.state.Connected = false
	c.state.Reading = nil
	c.state.Message = "已確認 OFF 並斷線"
	return nil
}
func (c *Controller) fail(err error) {
	// A timed-out socket may contain delayed responses. Never call OFF? on a dirty
	// stream and mistake an old response for confirmation. Send OFF once and close.
	hadConnection := c.conn != nil
	if hadConnection {
		c.conn.SetWriteDeadline(time.Now().Add(time.Second))
		fmt.Fprint(c.conn, "OUTP OFF\n")
		c.log("TX OUTP OFF (錯誤後盡力送出，未確認)")
		c.conn.Close()
		c.conn = nil
		c.reader = nil
		c.state.Unknown = true
	}
	c.state.Connected = false
	c.state.Config = nil
	c.state.Reading = nil
	c.state.Error = err.Error()
	if c.state.Unknown {
		c.state.Error += "；OFF 未確認，請用儀器面板關閉輸出"
	}
	c.state.Message = c.state.Error
	c.log("ERROR " + c.state.Error)
}
func (c *Controller) sample() error {
	var r Reading
	var e error
	if r.Voltage, e = c.number("MEAS:VOLT?"); e != nil {
		return e
	}
	if r.Current, e = c.number("MEAS:CURR?"); e != nil {
		return e
	}
	if r.Power, e = c.number("MEAS:POW?"); e != nil {
		return e
	}
	if r.Output, e = c.flag("OUTP?"); e != nil {
		return e
	}
	if r.Emulation, e = c.query("EMUL?"); e != nil {
		return e
	}
	bits, e := c.bits("STAT:OPER:COND?")
	if e != nil {
		return e
	}
	if r.Questionable, e = c.bits("STAT:QUES:COND?"); e != nil {
		return e
	}
	if e = c.errors(); e != nil {
		return e
	}
	r.Actual = "—"
	if bits&1 != 0 {
		r.Actual = "CV"
	}
	if bits&2 != 0 {
		r.Actual = "CC"
	}
	if !r.Output {
		r.Actual = "OFF"
	}
	r.Time = time.Now().Format(time.RFC3339)
	if r.Output && c.state.Config != nil {
		if math.Abs(r.Voltage) > 30.1 || math.Abs(r.Current) > 20.1 || math.Abs(r.Power) > 200.5 {
			return errors.New("實測超過本版本 30 V／20 A／200 W 軟體監控限制")
		}
		target := "PSUP"
		if c.state.Config.Role == "sink" {
			target = "LOAD"
		}
		if r.Emulation != target {
			return errors.New("儀器模式被其他控制端變更")
		}
	}
	c.state.Reading = &r
	c.state.History = append(c.state.History, r)
	if len(c.state.History) > 120 {
		c.state.History = c.state.History[len(c.state.History)-120:]
	}
	return nil
}
func (c *Controller) poll() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for range ticker.C {
		c.mu.Lock()
		if c.conn != nil {
			c.begin()
			var e error
			if time.Since(time.Unix(0, c.heartbeat.Load())) > 10*time.Second {
				e = c.disconnect()
				if e == nil {
					c.state.Message = "網頁超過 10 秒未回報，已確認 OFF 並斷線"
				}
			} else {
				e = c.sample()
			}
			if e != nil {
				c.fail(e)
			}
		}
		c.mu.Unlock()
	}
}
