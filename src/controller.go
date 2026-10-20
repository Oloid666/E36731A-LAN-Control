package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Config struct {
	Role        string  `json:"role"`
	Mode        string  `json:"mode"`
	Voltage     float64 `json:"voltage"`
	Current     float64 `json:"current"`
	OVP         float64 `json:"ovp"`
	Resistance  float64 `json:"resistance"`
	ESRMilliohm float64 `json:"esr_milliohm"`
	MinVoltage  float64 `json:"min_voltage"`
	MaxVoltage  float64 `json:"max_voltage"`
}
type Reading struct {
	Time         string   `json:"time"`
	Voltage      float64  `json:"voltage"`
	Current      float64  `json:"current"`
	Power        float64  `json:"power"`
	Resistance   *float64 `json:"resistance"`
	SampleMS     float64  `json:"sample_ms"`
	Output       bool     `json:"output"`
	Actual       string   `json:"actual"`
	Emulation    string   `json:"emulation"`
	Questionable int      `json:"questionable"`
}
type State struct {
	DebugOnly    bool              `json:"debug_only"`
	Preferences  Preferences       `json:"preferences"`
	Connected    bool              `json:"connected"`
	Identity     string            `json:"identity"`
	IP           string            `json:"ip"`
	Config       *Config           `json:"config"`
	Reading      *Reading          `json:"reading"`
	History      []Reading         `json:"history"`
	Logs         []string          `json:"logs"`
	Message      string            `json:"message"`
	Error        string            `json:"error"`
	Unknown      bool              `json:"unknown"`
	Warning      string            `json:"warning"`
	LastCommand  string            `json:"last_command"`
	LastResponse string            `json:"last_response"`
	LastQueryMS  float64           `json:"last_query_ms"`
	LastFailure  string            `json:"last_failure"`
	Version      string            `json:"version"`
	Options      ConnectionOptions `json:"options"`
	Diagnostics  []DiagnosticItem  `json:"diagnostics"`
}
type Controller struct {
	preferencesPath string
	mu              sync.Mutex
	conn            net.Conn
	reader          *bufio.Reader
	dial            func(string, string, time.Duration) (net.Conn, error)
	state           State
	generation      atomic.Int64
	ticket          int64
	deadline        time.Time
	heartbeat       atomic.Int64
	busy            atomic.Bool
	dirty           bool
	logWriter       io.Writer
	nextPoll        time.Time
}

func NewController() *Controller {
	c := &Controller{dial: net.DialTimeout}
	c.state.Message = "未連線；實機驗證尚未完成"
	c.state.Version = "v0.4.0"
	c.state.Options = DefaultOptions()
	c.state.Preferences = DefaultPreferences()
	c.heartbeat.Store(time.Now().UnixNano())
	return c
}
func (c *Controller) log(s string) {
	line := time.Now().Format("15:04:05.000 ") + s
	c.state.Logs = append(c.state.Logs, line)
	if c.logWriter != nil {
		fmt.Fprintln(c.logWriter, line)
	}
	if len(c.state.Logs) > 2000 {
		c.state.Logs = c.state.Logs[len(c.state.Logs)-2000:]
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
	if p.Role != "source" && p.Role != "sink" && p.Role != "battery" {
		return errors.New("Role 必須為 source、sink 或 battery")
	}
	if (p.Role == "battery" && p.Mode != "CV") || (p.Role != "battery" && p.Mode != "CV" && p.Mode != "CC" && !(p.Role == "sink" && p.Mode == "CR")) {
		return errors.New("Source 支援 CC/CV；Sink 支援 CC/CV/CR；Battery 僅支援 PS2Q CV（未設定 ESR）")
	}
	if p.ESRMilliohm != 0 {
		return errors.New("原生硬體 ESR 介面尚未確認，不能套用 ESR；低速軟體模擬已移除。ESR=0 僅代表不請求設定內阻，不代表儀器實際內阻為零")
	}
	for _, v := range []float64{p.Voltage, p.Current, p.OVP, p.Resistance, p.ESRMilliohm, p.MinVoltage, p.MaxVoltage} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return errors.New("參數必須為有限數值")
		}
	}
	if p.Mode != "CR" && (p.Current <= 0 || p.Current > 20) {
		return errors.New("本版本電流範圍 >0–20 A")
	}
	if p.Role == "source" || p.Role == "battery" {
		if p.Voltage < 0 || p.Voltage > 30 || p.OVP <= p.Voltage || p.OVP > 33 {
			return errors.New("Source：電壓 0–30 V，OVP 高於電壓且不超過 33 V")
		}
	} else {
		if p.Mode != "CR" && p.Current < 0.01 {
			return errors.New("Sink 高電流檔最小設定為 0.01 A")
		}
		if p.Mode == "CV" && (p.Voltage < 0.02 || p.Voltage > 30) {
			return errors.New("Sink CV：電壓範圍 0.02–30 V")
		}
	}
	if p.Mode == "CR" && (p.Resistance < 0.08 || p.Resistance > 4000) {
		return errors.New("Sink CR 阻值範圍為 0.08–4000 Ω")
	}
	if p.Role == "battery" {
		if p.Current < 0.01 {
			return errors.New("PS2Q 電流設定最低 0.01 A")
		}
		if p.MinVoltage < 0 || p.MinVoltage >= p.MaxVoltage || p.MaxVoltage > 30 || p.Voltage < p.MinVoltage || p.Voltage > p.MaxVoltage || p.MaxVoltage >= p.OVP {
			return errors.New("PS2Q 必須符合 0≤下限≤Voc≤上限≤30 V，且上限<OVP")
		}
		if p.MaxVoltage*p.Current > 200 {
			return errors.New("PS2Q 上限電壓×電流超過 200 W")
		}
	}
	if (p.Role == "source" || p.Role == "battery" || p.Mode == "CV") && p.Voltage*p.Current > 200 {
		return errors.New("本版本設定 V×I 上限為 200 W")
	}
	return nil
}
func (c *Controller) begin() {
	c.ticket = c.generation.Load()
	c.deadline = time.Now().Add(time.Duration(c.state.Options.TimeoutMS*12) * time.Millisecond)
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
	d := time.Now().Add(time.Duration(c.state.Options.TimeoutMS) * time.Millisecond)
	if c.deadline.Before(d) {
		d = c.deadline
	}
	return c.conn.SetDeadline(d)
}
func (c *Controller) write(cmd string) error {
	if err := c.ready(); err != nil {
		return err
	}
	c.state.LastCommand = cmd
	c.state.LastResponse = ""
	c.log("TX " + cmd)
	_, err := fmt.Fprint(c.conn, cmd+"\n")
	if err != nil {
		c.dirty = true
	}
	return err
}
func (c *Controller) query(cmd string) (string, error) {
	started := time.Now()
	if err := c.write(cmd); err != nil {
		return "", err
	}
	c.dirty = true
	// Bound the line so a wrong TCP service cannot allocate arbitrary memory.
	var line []byte
	for len(line) < 4096 {
		b, err := c.reader.ReadByte()
		if err != nil {
			return "", err
		}
		if b == '\n' {
			s := strings.TrimSpace(string(line))
			c.dirty = false
			c.state.LastResponse = s
			c.state.LastQueryMS = float64(time.Since(started).Microseconds()) / 1000
			c.log(fmt.Sprintf("RX [%.1f ms] %s", c.state.LastQueryMS, s))
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
func (c *Controller) readErrors() ([]string, error) {
	var list []string
	for i := 0; i < 21; i++ {
		response, e := c.query("SYST:ERR?")
		if e != nil {
			return list, e
		}
		code, e := strconv.Atoi(strings.TrimSpace(strings.SplitN(response, ",", 2)[0]))
		if e != nil {
			return list, fmt.Errorf("錯誤佇列格式錯誤：%s", response)
		}
		if code == 0 {
			return list, nil
		}
		list = append(list, response)
	}
	return list, errors.New("錯誤佇列未清空：" + strings.Join(list, " / "))
}
func (c *Controller) errors() error {
	list, e := c.readErrors()
	if e != nil {
		return e
	}
	if len(list) > 0 {
		return errors.New("SCPI 新錯誤：" + strings.Join(list, " / "))
	}
	return nil
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
	c.state.Warning = ""
	c.state.Diagnostics = nil
	conn, e := c.dial("tcp", net.JoinHostPort(ip, "5025"), time.Duration(c.state.Options.TimeoutMS)*time.Millisecond)
	if e != nil {
		return e
	}
	c.conn = conn
	c.reader = bufio.NewReader(conn)
	c.dirty = false
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
	baseline, e := c.readErrors()
	if e != nil {
		return e
	}
	if len(baseline) > 0 {
		c.state.Warning = "連線時已存在的 SCPI 錯誤（來源未確認，已逐筆記錄）：" + strings.Join(baseline, " / ")
		c.log("BASELINE " + c.state.Warning)
	}
	c.state.Connected = true
	c.state.IP = ip
	c.state.Identity = identity
	c.state.Unknown = false
	c.state.History = nil
	c.state.Message = "已連線；未變更設定或開啟輸出"
	c.heartbeat.Store(time.Now().UnixNano())
	if c.state.DebugOnly {
		c.state.Message = "唯讀診斷連線；不輪詢、不設定、不允許 ON"
		return nil
	}
	return c.sample()
}
func (c *Controller) apply(p Config) error {
	if c.state.DebugOnly {
		return errors.New("唯讀診斷連線禁止設定；請先斷線，再用一般連線")
	}
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
	} else if p.Role == "battery" {
		target = "PS2Q"
	}
	mode, e := c.query("EMUL?")
	if e != nil {
		return e
	}
	if mode != target {
		if e = c.switchMode(target); e != nil {
			return e
		}
	}
	commands := []string{"SYST:REM"}
	if p.Role != "battery" {
		commands = append(commands, "INIT:CONT:TRAN OFF", "ABOR:TRAN", "VOLT:MODE FIX", "CURR:MODE FIX", "OUTP:DEL:RISE 0", "OUTP:DEL:FALL 0")
	}
	if p.Role == "battery" {
		commands = append(commands, fmt.Sprintf("VOLT:PROT %.9g", p.OVP), "VOLT:PROT:STAT ON", fmt.Sprintf("CURR %.9g", p.Current), fmt.Sprintf("VOLT %.9g", p.Voltage))
	} else if p.Role == "source" {
		preferred := "VOLT"
		if p.Mode == "CC" {
			preferred = "CURR"
		}
		commands = append(commands, "OUTP:PMOD "+preferred, fmt.Sprintf("VOLT:PROT %.9g", p.OVP), "VOLT:PROT:STAT ON", fmt.Sprintf("CURR %.9g", p.Current), fmt.Sprintf("VOLT %.9g", p.Voltage))
	} else {
		// RES is electronic-load input resistance, not battery internal resistance.
		commands = append(commands, "CURR:RANG MAX")
		if p.Mode == "CV" {
			commands = append(commands, "VOLT:RANG MAX", "FUNC VOLT", fmt.Sprintf("CURR:LIM %.9g", p.Current), fmt.Sprintf("VOLT %.9g", p.Voltage))
		} else if p.Mode == "CR" {
			limit := 4000.0
			if p.Resistance <= 30 {
				limit = 30
			} else if p.Resistance <= 1250 {
				limit = 1250
			}
			// Manual p.105: level and range in one SCPI message prevent range conflicts.
			commands = append(commands, "FUNC RES", "RES:MODE FIX", fmt.Sprintf("RES:RANG %.9g;:RES %.9g", limit, p.Resistance))
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
	} else if p.Role == "battery" {
		expected = "PS2Q"
	}
	s, e := c.query("EMUL?")
	if e != nil {
		return e
	}
	if s != expected {
		return errors.New("操作模式讀回不符")
	}
	if p.Role == "battery" {
		return c.verifyPS2Q(p)
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
		} else if p.Mode == "CR" {
			if f != "RES" {
				return errors.New("Sink 不在 CR 模式")
			}
			if e = c.compare("RES?", p.Resistance); e != nil {
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
	if c.state.DebugOnly {
		return errors.New("唯讀診斷連線禁止 ON")
	}
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
		expectedCurrent := expected.Current
		if expected.Mode == "CR" {
			expectedCurrent = math.Abs(v) / expected.Resistance
		}
		if math.Abs(v) > 30 || expectedCurrent > 20 || math.Abs(v)*expectedCurrent > 200 {
			return errors.New("輸入電壓或預估功率超過本版本 30 V／200 W 限制")
		}
	}
	if expected.Role == "battery" {
		v, e := c.number("MEAS:VOLT?")
		if e != nil {
			return e
		}
		if v > expected.MaxVoltage+.01 || v < -.01 {
			return errors.New("PS2Q ON 前外部電壓超出允許範圍")
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
	failedCommand := c.state.LastCommand
	c.state.LastFailure = fmt.Sprintf("%s → %v", failedCommand, err)
	hadConnection := c.conn != nil
	confirmedOff := false
	if hadConnection {
		if !c.dirty {
			c.begin()
			confirmedOff = c.off() == nil
		}
		if !confirmedOff {
			c.conn.SetWriteDeadline(time.Now().Add(time.Second))
			fmt.Fprint(c.conn, "OUTP OFF\n")
			c.log("TX OUTP OFF (錯誤後盡力送出，未確認)")
		}
		c.conn.Close()
		c.conn = nil
		c.reader = nil
		c.state.Unknown = !confirmedOff
	}
	c.state.Connected = false
	c.state.Config = nil
	c.state.Reading = nil
	c.state.Error = c.state.LastFailure
	if confirmedOff {
		c.state.Error += "；已確認 OFF 並斷線"
	} else if c.state.Unknown {
		c.state.Error += "；OFF 未確認，請用儀器面板關閉輸出"
	}
	c.state.Message = c.state.Error
	c.log("ERROR " + c.state.Error)
}
func (c *Controller) sample() error {
	started := time.Now()
	var r Reading
	var e error
	if r.Voltage, e = c.number("MEAS:VOLT?"); e != nil {
		return e
	}
	if r.Current, e = c.number("MEAS:CURR?"); e != nil {
		return e
	}
	// Sequential V/I readings: calculated power, not instrument-averaged power.
	// Firmware in the field timed out on MEAS:POW?; do not issue this query.
	r.Power = r.Voltage * r.Current
	if r.Output, e = c.flag("OUTP?"); e != nil {
		return e
	}
	if r.Emulation, e = c.query("EMUL?"); e != nil {
		return e
	}
	if r.Emulation == "LOAD" {
		resistance, err := c.number("RES?")
		if err != nil {
			return err
		}
		r.Resistance = &resistance
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
	if bits&4 != 0 {
		r.Actual = "CR"
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
		} else if c.state.Config.Role == "battery" {
			target = "PS2Q"
		}
		if r.Emulation != target {
			return errors.New("儀器模式被其他控制端變更")
		}
	}
	if r.Output && c.state.Config != nil && c.state.Config.Role == "battery" {
		if e = c.monitorPS2Q(&r); e != nil {
			return e
		}
	}
	r.SampleMS = float64(time.Since(started).Microseconds()) / 1000
	c.state.Reading = &r
	c.state.History = append(c.state.History, r)
	if len(c.state.History) > 120 {
		c.state.History = c.state.History[len(c.state.History)-120:]
	}
	return nil
}
func (c *Controller) poll() {
	ticker := time.NewTicker(200 * time.Millisecond)
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
			} else if !c.state.DebugOnly && time.Now().After(c.nextPoll) {
				e = c.sample()
				c.nextPoll = time.Now().Add(time.Duration(c.state.Options.PollMS) * time.Millisecond)
			}
			if e != nil {
				c.fail(e)
			}
		}
		c.mu.Unlock()
	}
}
