//go:build linux

package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"
)

func TestHelper(t *testing.T) {
	if os.Getenv("ONDEMAND_TEST_HELPER") != "1" {
		return
	}
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			time.Sleep(300 * time.Millisecond)
		}
		w.Write([]byte("backend"))
	})
	http.ListenAndServe("127.0.0.1:"+os.Getenv("PORT"), nil)
	os.Exit(0)
}
func fixture(t *testing.T) *supervisor {
	t.Helper()
	t.Setenv("ONDEMAND_TEST_HELPER", "1")
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	addr := l.Addr().String()
	l.Close()
	s := &supervisor{target: addr, command: []string{os.Args[0], "-test.run=^TestHelper$"}, idle: 20 * time.Millisecond, startup: 3 * time.Second, last: time.Now()}
	t.Cleanup(s.close)
	return s
}
func TestLifecycle(t *testing.T) {
	s := fixture(t)
	server := httptest.NewServer(s.handler())
	defer server.Close()
	r, e := http.Get(server.URL + "/_ondemand/health")
	if e != nil {
		t.Fatal(e)
	}
	r.Body.Close()
	if s.cmd != nil {
		t.Fatal("health started backend")
	}
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := http.Get(server.URL)
			if e != nil {
				t.Error(e)
				return
			}
			defer r.Body.Close()
			b, _ := io.ReadAll(r.Body)
			if string(b) != "backend" {
				t.Errorf("body: %s", b)
			}
		}()
	}
	wg.Wait()
	s.mu.Lock()
	pid := s.cmd.Process.Pid
	s.mu.Unlock()
	if e := s.acquire(context.Background()); e != nil {
		t.Fatal(e)
	}
	time.Sleep(30 * time.Millisecond)
	s.sweep()
	if s.cmd == nil {
		t.Fatal("stopped active worker")
	}
	s.release()
	time.Sleep(30 * time.Millisecond)
	s.sweep()
	if s.cmd != nil {
		t.Fatal("idle worker survived")
	}
	if e := s.acquire(context.Background()); e != nil {
		t.Fatal(e)
	}
	if s.cmd.Process.Pid == pid {
		t.Fatal("did not restart")
	}
	s.release()
}
func TestFailedStart(t *testing.T) {
	s := fixture(t)
	s.command = []string{"/does-not-exist"}
	if e := s.acquire(context.Background()); e == nil {
		t.Fatal("expected failure")
	}
	if s.active != 0 {
		t.Fatal("leaked active count")
	}
}
func TestCancelledStartup(t *testing.T) {
	s := fixture(t)
	s.command = []string{"sleep", "30"}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if e := s.acquire(ctx); e == nil {
		t.Fatal("expected cancellation")
	}
	if s.cmd != nil || s.active != 0 {
		t.Fatal("leaked process")
	}
}
