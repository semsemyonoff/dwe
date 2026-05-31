package builtin

import (
	"context"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/semsemyonoff/devbox/internal/core/execution/builtin/spec"
)

func TestTCPReachableValidate(t *testing.T) {
	t.Parallel()
	b := TCPReachable{}
	cases := []struct {
		name    string
		with    map[string]any
		wantErr string
	}{
		{"ok", map[string]any{"host": "localhost", "port": 80}, ""},
		{"port float", map[string]any{"host": "localhost", "port": 80.0}, ""},
		{"missing host", map[string]any{"port": 80}, "missing required param 'host'"},
		{"missing port", map[string]any{"host": "x"}, "missing required param"},
		{"port too low", map[string]any{"host": "x", "port": 0}, "out of range"},
		{"port too high", map[string]any{"host": "x", "port": 70000}, "out of range"},
		{"bad timeout", map[string]any{"host": "x", "port": 80, "timeout": "nope"}, "invalid duration"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			err := b.Validate(tt.with)
			if tt.wantErr == "" && err != nil {
				t.Fatalf("unexpected: %v", err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("want %q, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestTCPReachableDescribe(t *testing.T) {
	t.Parallel()
	got := TCPReachable{}.Describe(map[string]any{"host": "h", "port": 1234})
	if !strings.Contains(got, "h") || !strings.Contains(got, "1234") {
		t.Fatalf("describe: %q", got)
	}
}

func TestTCPReachableRun(t *testing.T) {
	t.Parallel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	port := ln.Addr().(*net.TCPAddr).Port

	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()

	b := TCPReachable{}
	if err := b.Run(context.Background(), map[string]any{"host": "127.0.0.1", "port": port}, spec.ExecContext{}); err != nil {
		t.Fatalf("connect: %v", err)
	}
}

func TestTCPReachableRunTimeout(t *testing.T) {
	t.Parallel()
	// 198.51.100.0/24 is TEST-NET-2 reserved for documentation; unroutable.
	b := TCPReachable{}
	start := time.Now()
	err := b.Run(context.Background(), map[string]any{"host": "198.51.100.1", "port": 81, "timeout": "100ms"}, spec.ExecContext{})
	if err == nil {
		t.Fatal("want dial error")
	}
	if !strings.Contains(err.Error(), "dial tcp") {
		t.Fatalf("want dial tcp error, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("timeout took too long: %v", elapsed)
	}
}

func TestGetIntParam(t *testing.T) {
	t.Parallel()
	cases := []struct {
		v       any
		want    int
		wantErr bool
	}{
		{int(5), 5, false},
		{int64(5), 5, false},
		{float64(5), 5, false},
		{float64(5.5), 0, true},
		{"7", 7, false},
		{"x", 0, true},
		{true, 0, true},
	}
	for i, tt := range cases {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			got, err := getIntParam(map[string]any{"k": tt.v}, "k")
			if tt.wantErr && err == nil {
				t.Fatal("want err")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected: %v", err)
			}
			if !tt.wantErr && got != tt.want {
				t.Fatalf("got %d want %d", got, tt.want)
			}
		})
	}
}
