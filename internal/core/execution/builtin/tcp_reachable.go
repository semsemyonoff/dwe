package builtin

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/semsemyonoff/devbox/internal/core/execution/builtin/spec"
)

const tcpDefaultTimeout = 3 * time.Second

// TCPReachable is the `tcp_reachable` predicate builtin. It reports success
// when a TCP dial to host:port completes within the configured timeout.
type TCPReachable struct{}

// Validate checks that host, port, and (optional) timeout are well-formed.
func (TCPReachable) Validate(with map[string]any) error {
	host := spec.GetStringParam(with, "host", "")
	if host == "" {
		return errors.New("missing required param 'host'")
	}
	port, err := getIntParam(with, "port")
	if err != nil {
		return err
	}
	if port < 1 || port > 65535 {
		return fmt.Errorf("param 'port': out of range 1-65535 (got %d)", port)
	}
	if _, err := spec.GetDurationParam(with, "timeout", tcpDefaultTimeout); err != nil {
		return err
	}
	return nil
}

// Describe returns a one-line summary for plan output.
func (TCPReachable) Describe(with map[string]any) string {
	host := spec.GetStringParam(with, "host", "")
	port, _ := getIntParam(with, "port")
	return fmt.Sprintf("builtin: tcp_reachable(host=%s, port=%d)", host, port)
}

// Run dials host:port with the configured timeout.
func (TCPReachable) Run(ctx context.Context, with map[string]any, _ spec.ExecContext) error {
	host := spec.GetStringParam(with, "host", "")
	port, err := getIntParam(with, "port")
	if err != nil {
		return err
	}
	timeout, err := spec.GetDurationParam(with, "timeout", tcpDefaultTimeout)
	if err != nil {
		return err
	}

	addr := net.JoinHostPort(host, strconv.Itoa(port))
	conn, dialErr := (&net.Dialer{Timeout: timeout}).DialContext(ctx, "tcp", addr)
	if dialErr != nil {
		return dialErr
	}
	_ = conn.Close()
	return nil
}

// getIntParam returns the integer value of key from with.
// Accepts int, int64, float64 (from YAML unmarshal), or string.
func getIntParam(with map[string]any, key string) (int, error) {
	if with == nil {
		return 0, fmt.Errorf("missing required param %q", key)
	}
	v, ok := with[key]
	if !ok || v == nil {
		return 0, fmt.Errorf("missing required param %q", key)
	}
	switch val := v.(type) {
	case int:
		return val, nil
	case int64:
		return int(val), nil
	case float64:
		if val != float64(int(val)) {
			return 0, fmt.Errorf("param %q: expected integer, got %v", key, val)
		}
		return int(val), nil
	case string:
		n, err := strconv.Atoi(val)
		if err != nil {
			return 0, fmt.Errorf("param %q: invalid integer %q", key, val)
		}
		return n, nil
	default:
		return 0, fmt.Errorf("param %q: expected integer, got %T", key, v)
	}
}
