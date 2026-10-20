package main

import (
	"bufio"
	"fmt"
	"net"
	"time"
)

// EMUL performs a reset. Never queue *OPC? on the transitioning session.
// Retry only read-only probes; the mode write is sent exactly once, while OFF.
func (c *Controller) switchMode(target string) error {
	if err := c.write("EMUL " + target); err != nil {
		return err
	}
	c.log("MODE 等待重設並重新連線；未傳送 *OPC? 或 ON")
	c.conn.Close()
	c.conn, c.reader = nil, nil
	c.state.Unknown = true
	previousDeadline := c.deadline
	defer func() { c.deadline = previousDeadline }()
	limit := time.Now().Add(20 * time.Second)
	if limit.Before(c.deadline) {
		c.deadline = limit
	}
	last := "尚未讀回模式"
	for time.Now().Before(c.deadline) {
		// A short, cancellable settling delay also spaces reconnect attempts.
		until := time.Now().Add(500 * time.Millisecond)
		for time.Now().Before(until) {
			if c.generation.Load() != c.ticket {
				return fmt.Errorf("模式切換取消：%w", errCancelled)
			}
			if time.Now().After(c.deadline) {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		remaining := time.Until(c.deadline)
		if remaining <= 0 {
			break
		}
		timeout := time.Duration(c.state.Options.TimeoutMS) * time.Millisecond
		if remaining < timeout {
			timeout = remaining
		}
		conn, err := c.dial("tcp", net.JoinHostPort(c.state.IP, "5025"), timeout)
		if err != nil {
			last = err.Error()
			c.log("MODE 重連失敗：" + last)
			continue
		}
		c.conn, c.reader, c.dirty = conn, bufio.NewReader(conn), false
		identity, err := c.query("*IDN?")
		if err == nil && identity != c.state.Identity {
			conn.Close()
			c.conn, c.reader = nil, nil
			return fmt.Errorf("模式切換後儀器身分不同；未傳送設定或輸出指令")
		}
		var mode string
		var on bool
		if err == nil {
			mode, err = c.query("EMUL?")
		}
		if err == nil {
			on, err = c.flag("OUTP?")
		}
		if err == nil {
			c.state.Unknown = on
			// Preserve and report new errors (including unsupported PS2Q).
			if e := c.errors(); e != nil {
				return fmt.Errorf("切換 %s，實際模式 %s：%w", target, mode, e)
			}
			if on {
				return fmt.Errorf("模式切換後輸出並非 OFF，停止設定")
			}
			if mode == target {
				c.log("MODE 已確認 " + target + "；OUTP=0；重新連線成功")
				return nil
			}
			last = "讀回模式 " + mode + "，預期 " + target
		} else {
			last = err.Error()
		}
		c.log("MODE 尚未完成：" + last)
		conn.Close()
		c.conn, c.reader = nil, nil
	}
	return fmt.Errorf("切換 %s 未確認成功：%s；未繼續套用設定", target, last)
}
