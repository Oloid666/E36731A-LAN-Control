package main

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"time"
)

// One explicit operator action: connect if needed, confirm OFF, then apply.
// It never enables output, including when reconnecting after a failure.
func (c *Controller) prepare(ip string, p Config, options ConnectionOptions) error {
	if e := validIP(ip); e != nil {
		return e
	}
	if e := p.validate(); e != nil {
		return e
	}
	opts, e := options.normalized()
	if e != nil {
		return e
	}
	if c.generation.Load() != c.ticket {
		return errCancelled
	}
	if c.conn != nil && c.state.IP != ip {
		return errors.New("請先 OFF 並斷線，再切換儀器 IP")
	}
	c.state.Options = opts
	if c.conn == nil {
		c.progress("一鍵套用：連接儀器 " + ip)
		c.state.DebugOnly = false
		if e = c.connect(ip); e != nil {
			return e
		}
	}
	c.progress("一鍵套用：關閉輸出並確認 OFF")
	if e = c.off(); e != nil {
		return e
	}
	// Clicking this explicit write action ends a read-only diagnostic session.
	c.state.DebugOnly = false
	c.progress("一鍵套用：設定模式與參數，請等待讀回")
	if e = c.apply(p); e != nil {
		return e
	}
	if e = c.savePreferences(Preferences{IP: ip, Config: p, Options: opts}); e != nil {
		c.log("SETTINGS " + e.Error())
	}
	c.state.Message = "已套用並讀回驗證，輸出 OFF；可按輸出 ON"
	return nil
}

// A mode reset may temporarily close the session. Re-identify exactly the
// original instrument before sending OFF; never send commands to a new model.
func (c *Controller) stopOutput() error {
	if c.conn == nil && c.state.Unknown {
		if c.state.Identity == "" || validIP(c.state.IP) != nil {
			return errors.New("無法識別原儀器，OFF 未確認；請用面板關閉輸出")
		}
		c.progress("重新連線確認 OFF")
		c.state.Connected = false
		conn, e := c.dial("tcp", net.JoinHostPort(c.state.IP, "5025"), time.Duration(c.state.Options.TimeoutMS)*time.Millisecond)
		if e != nil {
			return e
		}
		c.conn, c.reader, c.dirty = conn, bufio.NewReader(conn), false
		identity, e := c.query("*IDN?")
		if e != nil || identity != c.state.Identity {
			conn.Close()
			c.conn, c.reader = nil, nil
			if e != nil {
				return e
			}
			return fmt.Errorf("重連後儀器身分不同，未傳送輸出指令；OFF 未確認")
		}
		c.state.Connected = true
	}
	return c.off()
}
