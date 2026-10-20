package main

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"
)

type commandError struct {
	Command string
	Err     error
}

func (e *commandError) Error() string { return fmt.Sprintf("%s → %v", e.Command, e.Err) }
func (e *commandError) Unwrap() error { return e.Err }

// Attribute an error queue entry to one command, never to a whole setup batch.
func (c *Controller) checkedWrite(cmd string) error {
	if e := c.write(cmd); e != nil {
		return &commandError{cmd, e}
	}
	if e := c.errors(); e != nil {
		return &commandError{cmd, e}
	}
	c.log("CHECK " + cmd + " → OK（無新 SCPI 錯誤）")
	return nil
}
func (c *Controller) checkedQuery(cmd string) (string, error) {
	s, e := c.query(cmd)
	if e != nil {
		return s, &commandError{cmd, e}
	}
	if e = c.errors(); e != nil {
		return s, &commandError{cmd, e}
	}
	return s, nil
}
func (c *Controller) checkedNumber(cmd string) (float64, error) {
	s, e := c.checkedQuery(cmd)
	if e != nil {
		return 0, e
	}
	v, e := strconv.ParseFloat(s, 64)
	if e != nil || math.IsNaN(v) || math.IsInf(v, 0) || math.Abs(v) > 1e30 {
		return 0, &commandError{cmd, fmt.Errorf("無效數值 %q", s)}
	}
	return v, nil
}
func (c *Controller) checkedFlag(cmd string) (bool, error) {
	v, e := c.checkedNumber(cmd)
	if e != nil {
		return false, e
	}
	if v != 0 && v != 1 {
		return false, &commandError{cmd, errors.New("回傳值不是 0 或 1")}
	}
	return v == 1, nil
}

type readbackMismatch struct {
	Command   string
	Got, Want float64
}

func (e *readbackMismatch) Error() string {
	return fmt.Sprintf("%s 讀回 %.9g，預期 %.9g", e.Command, e.Got, e.Want)
}
func (c *Controller) checkedCompare(cmd string, want float64) error {
	got, e := c.checkedNumber(cmd)
	if e != nil {
		return e
	}
	if math.Abs(got-want) > math.Max(.002, math.Abs(want)*1e-4) {
		return &readbackMismatch{cmd, got, want}
	}
	return nil
}
func (c *Controller) waitPS2QReadback(p Config) error {
	previousDeadline := c.deadline
	defer func() { c.deadline = previousDeadline }()
	limit := time.Now().Add(10 * time.Second)
	if c.deadline.Before(limit) {
		limit = c.deadline
	}
	c.deadline = limit
	for {
		e := c.verify(p)
		if e == nil {
			return nil
		}
		var mismatch *readbackMismatch
		if !errors.As(e, &mismatch) {
			return e
		}
		if time.Now().After(limit) {
			return fmt.Errorf("PS2Q 讀回未在期限內到位：%w", e)
		}
		c.progress("PS2Q 等待參數讀回到位；不使用 *OPC?：" + e.Error())
		until := time.Now().Add(100 * time.Millisecond)
		for time.Now().Before(until) {
			if c.generation.Load() != c.ticket {
				return errCancelled
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}
