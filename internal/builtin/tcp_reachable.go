package builtin

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"
)

const tcpDefaultTimeout = 3 * time.Second

type tcpReachableBuiltin struct{}

func (tcpReachableBuiltin) Validate(with map[string]any) error {
	host := getStringParam(with, "host", "")
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
	if _, err := getDurationParam(with, "timeout", tcpDefaultTimeout); err != nil {
		return err
	}
	return nil
}

func (tcpReachableBuiltin) Describe(with map[string]any) string {
	host := getStringParam(with, "host", "")
	port, _ := getIntParam(with, "port")
	return fmt.Sprintf("builtin: tcp_reachable(host=%s, port=%d)", host, port)
}

func (tcpReachableBuiltin) Run(_ context.Context, with map[string]any, _ ExecContext) error {
	host := getStringParam(with, "host", "")
	port, err := getIntParam(with, "port")
	if err != nil {
		return err
	}
	timeout, err := getDurationParam(with, "timeout", tcpDefaultTimeout)
	if err != nil {
		return err
	}

	addr := net.JoinHostPort(host, strconv.Itoa(port))
	conn, dialErr := net.DialTimeout("tcp", addr, timeout)
	if dialErr != nil {
		return fmt.Errorf("dial tcp %s: %s", addr, dialErr.Error())
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
