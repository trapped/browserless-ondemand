//go:build linux

package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type supervisor struct {
	mu            sync.Mutex
	cmd           *exec.Cmd
	done          chan struct{}
	active        int
	last          time.Time
	target        string
	command       []string
	idle, startup time.Duration
	closed        bool
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
func duration(key, fallback string) time.Duration {
	d, e := time.ParseDuration(env(key, fallback))
	if e != nil || d <= 0 {
		log.Fatalf("invalid %s", key)
	}
	return d
}
func (s *supervisor) acquire(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("shutting down")
	}
	if s.cmd != nil {
		select {
		case <-s.done:
			s.stopLocked()
		default:
		}
	}
	if s.cmd == nil {
		cmd := exec.Command(s.command[0], s.command[1:]...)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		for _, v := range os.Environ() {
			if !strings.HasPrefix(v, "PORT=") && !strings.HasPrefix(v, "HOST=") {
				cmd.Env = append(cmd.Env, v)
			}
		}
		_, port, _ := net.SplitHostPort(s.target)
		cmd.Env = append(cmd.Env, "PORT="+port, "HOST=127.0.0.1")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if e := cmd.Start(); e != nil {
			return e
		}
		s.cmd = cmd
		s.done = make(chan struct{})
		done := s.done
		go func() {
			e := cmd.Wait()
			if e != nil {
				log.Printf("browserless exited: %v", e)
			}
			close(done)
		}()
		log.Print("starting browserless")
		deadline := time.NewTimer(s.startup)
		defer deadline.Stop()
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
	ready:
		for {
			select {
			case <-ctx.Done():
				s.stopLocked()
				return ctx.Err()
			case <-s.done:
				s.stopLocked()
				return errors.New("browserless exited before readiness")
			case <-deadline.C:
				s.stopLocked()
				return errors.New("browserless startup timed out")
			case <-ticker.C:
				c, e := net.DialTimeout("tcp", s.target, 100*time.Millisecond)
				if e == nil {
					c.Close()
					break ready
				}
			}
		}
	}
	s.active++
	return nil
}
func (s *supervisor) release() { s.mu.Lock(); defer s.mu.Unlock(); s.active--; s.last = time.Now() }
func (s *supervisor) stopLocked() {
	if s.cmd == nil {
		return
	}
	pid := s.cmd.Process.Pid
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	select {
	case <-s.done:
	case <-time.After(10 * time.Second):
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		<-s.done
	}
	// Remove any remaining descendants (Xvfb/Chromium) in this process group.
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	s.cmd = nil
	log.Print("browserless stopped")
}
func (s *supervisor) sweep() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active == 0 && time.Since(s.last) >= s.idle {
		s.stopLocked()
	}
}
func (s *supervisor) close() { s.mu.Lock(); defer s.mu.Unlock(); s.closed = true; s.stopLocked() }
func (s *supervisor) handler() http.Handler {
	u, _ := url.Parse("http://" + s.target)
	p := httputil.NewSingleHostReverseProxy(u)
	p.Transport = &http.Transport{Proxy: nil, MaxIdleConns: 10, IdleConnTimeout: 5 * time.Second, ResponseHeaderTimeout: 0}
	p.ErrorHandler = func(w http.ResponseWriter, r *http.Request, e error) {
		log.Printf("upstream: %v", e)
		http.Error(w, "browserless unavailable", http.StatusBadGateway)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/_ondemand/health" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintln(w, `{"status":"ok"}`)
			return
		}
		if e := s.acquire(r.Context()); e != nil {
			log.Printf("startup: %v", e)
			http.Error(w, "browserless unavailable", http.StatusServiceUnavailable)
			return
		}
		defer s.release()
		p.ServeHTTP(w, r)
	})
}
func main() {
	port := env("PORT", "3000")
	backend := env("ONDEMAND_BACKEND_PORT", "3001")
	for _, p := range []string{port, backend} {
		n, e := strconv.Atoi(p)
		if e != nil || n < 1 || n > 65535 {
			log.Fatal("invalid port")
		}
	}
	if port == backend {
		log.Fatal("frontend and backend ports must differ")
	}
	command := os.Args[1:]
	if len(command) == 0 {
		command = []string{"./scripts/start.sh"}
	}
	s := &supervisor{target: "127.0.0.1:" + backend, command: command, idle: duration("ONDEMAND_IDLE_TIMEOUT", "60s"), startup: duration("ONDEMAND_START_TIMEOUT", "60s"), last: time.Now()}
	server := &http.Server{Addr: net.JoinHostPort(env("HOST", "0.0.0.0"), port), Handler: s.handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.sweep()
			}
		}
	}()
	go func() { <-ctx.Done(); s.close(); _ = server.Close() }()
	log.Printf("listening on %s; idle timeout %s", server.Addr, s.idle)
	if e := server.ListenAndServe(); e != nil && !errors.Is(e, http.ErrServerClosed) {
		log.Fatal(e)
	}
	s.close()
}
