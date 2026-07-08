package builtin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/semsemyonoff/dwe/internal/core/execution/builtin/spec"
)

const (
	httpDefaultStatus   = 200
	httpDefaultRetries  = 0
	httpDefaultInterval = 1 * time.Second
	httpDefaultTimeout  = 5 * time.Second

	// httpBodyReadCap bounds how much of a response body the `contains` check
	// reads into memory, so a misbehaving/hostile endpoint returning a huge
	// body cannot exhaust memory during a substring check.
	httpBodyReadCap = 8 << 20 // 8 MiB
)

// HTTPCheck is the `http_check` predicate builtin. It reports success when an
// HTTP GET to the configured url returns the expected status (and, when set,
// a body containing the `contains` substring), retrying up to `retries` times
// with `interval` between attempts. Each attempt is bounded by `timeout`.
type HTTPCheck struct{}

// Validate checks that url, status, contains, retries, interval, and timeout
// are well-formed before the pipeline runs.
func (HTTPCheck) Validate(with map[string]any) error {
	raw := spec.GetStringParam(with, "url", "")
	if raw == "" {
		return errors.New("missing required param 'url'")
	}
	if err := validateHTTPURL(raw); err != nil {
		return err
	}
	if _, err := getOptionalIntParam(with, "status", httpDefaultStatus); err != nil {
		return err
	}
	retries, err := getOptionalIntParam(with, "retries", httpDefaultRetries)
	if err != nil {
		return err
	}
	if retries < 0 {
		return fmt.Errorf("param 'retries': must be >= 0 (got %d)", retries)
	}
	interval, err := spec.GetDurationParam(with, "interval", httpDefaultInterval)
	if err != nil {
		return err
	}
	if interval < 0 {
		return fmt.Errorf("param 'interval': must be >= 0 (got %s)", interval)
	}
	timeout, err := spec.GetDurationParam(with, "timeout", httpDefaultTimeout)
	if err != nil {
		return err
	}
	if timeout <= 0 {
		return fmt.Errorf("param 'timeout': must be > 0 (got %s)", timeout)
	}
	return nil
}

// Describe returns a one-line summary for plan output.
func (HTTPCheck) Describe(with map[string]any) string {
	rawURL := spec.GetStringParam(with, "url", "")
	status, _ := getOptionalIntParam(with, "status", httpDefaultStatus)
	return fmt.Sprintf("builtin: http_check(url=%s, status=%d)", rawURL, status)
}

// Run performs the HTTP GET(s) and asserts status (and optional body substring).
func (HTTPCheck) Run(ctx context.Context, with map[string]any, _ spec.ExecContext) error {
	rawURL := spec.GetStringParam(with, "url", "")
	if rawURL == "" {
		return errors.New("missing required param 'url'")
	}
	if err := validateHTTPURL(rawURL); err != nil {
		return err
	}
	wantStatus, err := getOptionalIntParam(with, "status", httpDefaultStatus)
	if err != nil {
		return err
	}
	contains := spec.GetStringParam(with, "contains", "")
	retries, err := getOptionalIntParam(with, "retries", httpDefaultRetries)
	if err != nil {
		return err
	}
	interval, err := spec.GetDurationParam(with, "interval", httpDefaultInterval)
	if err != nil {
		return err
	}
	timeout, err := spec.GetDurationParam(with, "timeout", httpDefaultTimeout)
	if err != nil {
		return err
	}

	client := &http.Client{}
	// retries is validated >= 0 at plan time; guard here so an out-of-band or
	// overflowing value can never yield a zero-attempt loop (which would return
	// a nil-wrapped error having performed no request).
	attempts := max(retries+1, 1)
	var lastErr error
	for attempt := range attempts {
		lastErr = httpAttempt(ctx, client, rawURL, wantStatus, contains, timeout)
		if lastErr == nil {
			return nil
		}
		if attempt < attempts-1 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(interval):
			}
		}
	}
	if attempts > 1 {
		return fmt.Errorf("http_check %s: %w (after %d attempts)", rawURL, lastErr, attempts)
	}
	return fmt.Errorf("http_check %s: %w", rawURL, lastErr)
}

// httpAttempt performs one GET request bounded by timeout and asserts the
// response status and (optional) body substring.
func httpAttempt(ctx context.Context, client *http.Client, rawURL string, wantStatus int, contains string, timeout time.Duration) error {
	attemptCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(attemptCtx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != wantStatus {
		return fmt.Errorf("expected status %d, got %d", wantStatus, resp.StatusCode)
	}
	if contains != "" {
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, httpBodyReadCap))
		if readErr != nil {
			return fmt.Errorf("reading body: %w", readErr)
		}
		if !strings.Contains(string(body), contains) {
			return fmt.Errorf("response body does not contain %q", contains)
		}
	}
	return nil
}

// validateHTTPURL checks that raw parses as an absolute http/https URL.
func validateHTTPURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("param 'url': invalid URL %q: %w", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("param 'url': must be http or https (got %q)", raw)
	}
	if u.Host == "" {
		return fmt.Errorf("param 'url': missing host (got %q)", raw)
	}
	return nil
}

// getOptionalIntParam returns the integer value of key from with, or defaultVal
// when the key is absent/nil. Unlike getIntParam it does not error on absence.
func getOptionalIntParam(with map[string]any, key string, defaultVal int) (int, error) {
	if with == nil {
		return defaultVal, nil
	}
	if v, ok := with[key]; !ok || v == nil {
		return defaultVal, nil
	}
	return getIntParam(with, key)
}
