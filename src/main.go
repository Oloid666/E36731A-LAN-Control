package main

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"sync/atomic"
	"time"
)

//go:embed index.html
var assets embed.FS

type Action struct {
	Action  string            `json:"action"`
	IP      string            `json:"ip"`
	Config  Config            `json:"config"`
	Options ConnectionOptions `json:"options"`
}
type Server struct {
	controller *Controller
	token      string
	host       string
	quit       chan struct{}
	closing    atomic.Bool
}

func (s *Server) authorized(r *http.Request) bool {
	if r.Host != s.host || r.Header.Get("X-Control-Token") != s.token {
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+s.host {
		return false
	}
	return true
}
func jsonReply(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}
func (s *Server) state(w http.ResponseWriter) {
	s.controller.mu.Lock()
	defer s.controller.mu.Unlock()
	jsonReply(w, 200, struct {
		State
		Busy bool `json:"busy"`
	}{s.controller.state, s.controller.busy.Load()})
}
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
	if r.Host != s.host {
		http.Error(w, "Host rejected", 403)
		return
	}
	if r.URL.Path == "/" && r.Method == "GET" {
		b, _ := assets.ReadFile("index.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(b)
		return
	}
	if !s.authorized(r) {
		http.Error(w, "Session or Origin rejected", 403)
		return
	}
	c := s.controller
	if r.URL.Path == "/api/heartbeat" && r.Method == "GET" {
		c.heartbeat.Store(time.Now().UnixNano())
		w.WriteHeader(204)
		return
	}
	if r.URL.Path == "/api/debug" && r.Method == "GET" {
		w.Header().Set("Content-Disposition", "attachment; filename=E36731A-debug.json")
		s.state(w)
		return
	}
	if r.URL.Path == "/api/state" && r.Method == "GET" {
		s.state(w)
		return
	}
	if r.URL.Path != "/api/action" || r.Method != "POST" {
		http.NotFound(w, r)
		return
	}
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		http.Error(w, "JSON required", 415)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var a Action
	if e := decoder.Decode(&a); e != nil {
		jsonReply(w, 400, map[string]string{"error": e.Error()})
		return
	}
	if e := decoder.Decode(&struct{}{}); e != io.EOF {
		http.Error(w, "Only one JSON object allowed", 400)
		return
	}
	allowed := map[string]bool{"connect": true, "connect_debug": true, "apply": true, "on": true, "off": true, "disconnect": true, "quit": true, "diagnose": true, "save": true}
	if !allowed[a.Action] {
		http.Error(w, "Unknown action", 400)
		return
	}
	// Validate user input before touching an already connected instrument.
	if a.Action == "connect" || a.Action == "connect_debug" {
		if e := validIP(a.IP); e != nil {
			jsonReply(w, 400, map[string]string{"error": e.Error()})
			return
		}
	}
	if a.Action == "apply" || a.Action == "on" || a.Action == "save" {
		if e := a.Config.validate(); e != nil {
			jsonReply(w, 400, map[string]string{"error": e.Error()})
			return
		}
	}
	if a.Action == "connect" || a.Action == "connect_debug" || a.Action == "save" {
		var e error
		a.Options, e = a.Options.normalized()
		if e != nil {
			jsonReply(w, 400, map[string]string{"error": e.Error()})
			return
		}
	}
	ticket := c.generation.Load()
	if s.closing.Load() && a.Action != "off" {
		jsonReply(w, 503, map[string]string{"error": "程式正在結束"})
		return
	}
	if a.Action == "quit" {
		s.closing.Store(true)
	}
	emergency := a.Action == "off" || a.Action == "quit" || a.Action == "disconnect"
	if emergency {
		c.generation.Add(1)
	} else {
		if !c.busy.CompareAndSwap(false, true) {
			jsonReply(w, 409, map[string]string{"error": "上一個操作尚未完成"})
			return
		}
		defer c.busy.Store(false)
	}
	c.mu.Lock()
	c.begin()
	if !emergency {
		c.ticket = ticket
	}
	var e error
	if s.closing.Load() && !emergency {
		e = errors.New("程式正在結束，操作已取消")
	} else {
		switch a.Action {
		case "connect", "connect_debug":
			c.state.DebugOnly = a.Action == "connect_debug"
			c.state.Options = a.Options
			e = c.connect(a.IP)
			if e == nil {
				p := c.state.Preferences
				p.IP = a.IP
				p.Options = a.Options
				if saveErr := c.savePreferences(p); saveErr != nil {
					c.log("SETTINGS " + saveErr.Error())
				}
			}
		case "apply":
			e = c.apply(a.Config)
			if e == nil {
				p := c.state.Preferences
				p.Config = a.Config
				if saveErr := c.savePreferences(p); saveErr != nil {
					c.log("SETTINGS " + saveErr.Error())
				}
			}
		case "diagnose":
			e = c.diagnose()
		case "save":
			e = c.savePreferences(Preferences{IP: a.IP, Config: a.Config, Options: a.Options})
			if e == nil {
				c.state.Message = "已儲存設定；未套用到儀器，也未開啟輸出"
			}
		case "on":
			e = c.on(a.Config)
		case "off":
			e = c.off()
		case "disconnect", "quit":
			e = c.disconnect()
		}
	}
	if e != nil && a.Action != "save" {
		c.fail(e)
	}
	message := c.state.Message
	if e != nil && a.Action == "save" {
		message = e.Error()
	}
	c.mu.Unlock()
	if a.Action == "quit" {
		select {
		case s.quit <- struct{}{}:
		default:
		}
	}
	if e != nil {
		jsonReply(w, 502, map[string]string{"error": message})
		return
	}
	jsonReply(w, 200, map[string]string{"message": message})
}
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if e := cmd.Start(); e == nil {
		go cmd.Wait()
	}
}
func main() {
	listener, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		fmt.Println(e)
		os.Exit(1)
	}
	random := make([]byte, 32)
	if _, e = rand.Read(random); e != nil {
		fmt.Println(e)
		os.Exit(1)
	}
	controller := NewController()
	closeStorage := setupStorage(controller)
	defer closeStorage()
	app := &Server{controller: controller, token: hex.EncodeToString(random), host: listener.Addr().String(), quit: make(chan struct{}, 1)}
	server := &http.Server{Handler: app, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
	url := "http://" + app.host + "/#" + app.token
	fmt.Println("E36731A LAN Control v0.3.2 — Windows 免安裝版 / Local HTML")
	fmt.Println("請保留這個視窗。若瀏覽器未自動開啟，複製下面完整網址：")
	fmt.Println(url)
	fmt.Println("退出請用網頁的『OFF 並結束程式』；強制關閉程序不能保證儀器 OFF。")
	go controller.poll()
	if os.Getenv("E36731A_NO_BROWSER") != "1" {
		openBrowser(url)
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt)
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		select {
		case <-signals:
		case <-app.quit:
		}
		app.closing.Store(true)
		controller.generation.Add(1)
		controller.mu.Lock()
		controller.begin()
		if e := controller.disconnect(); e != nil {
			controller.fail(e)
			fmt.Println(controller.state.Error)
		}
		if controller.state.Unknown {
			fmt.Println("OFF 未確認：請立即檢查實機面板！")
			time.Sleep(3 * time.Second)
		}
		controller.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		server.Shutdown(ctx)
	}()
	if e = server.Serve(listener); e != nil && !errors.Is(e, http.ErrServerClosed) {
		fmt.Println(e)
		os.Exit(1)
	}
	<-shutdownDone
}
