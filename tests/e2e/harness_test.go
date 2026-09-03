//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

var (
	// httpBaseURL is how the Go test process reaches the server (always
	// loopback — the server runs in this same container).
	httpBaseURL string
	// browserBaseURL is how the remote browser reaches the server. When the
	// browser lives in a sibling container it must use this container's
	// network name, not 127.0.0.1.
	browserBaseURL string
	// cdpURL is the Chrome DevTools endpoint of the browser container.
	cdpURL string
	// artifactDir receives screenshots and HTML dumps for failing tests. It
	// lives in the OS temp dir, never in the project directory (PART 28).
	artifactDir string

	serverCmd *exec.Cmd
)

// TestMain boots the server binary under test, waits for it to report
// healthy, runs the suite, and always tears the server back down.
func TestMain(m *testing.M) {
	if err := setup(); err != nil {
		fmt.Fprintf(os.Stderr, "e2e setup failed: %v\n", err)
		teardown()
		os.Exit(1)
	}
	code := m.Run()
	teardown()
	os.Exit(code)
}

func setup() error {
	bin := os.Getenv("E2E_BIN")
	if bin == "" {
		return fmt.Errorf("E2E_BIN is not set (run this suite through tests/e2e.sh)")
	}
	if _, err := os.Stat(bin); err != nil {
		return fmt.Errorf("E2E_BIN %q: %w", bin, err)
	}

	cdpURL = os.Getenv("E2E_CDP_URL")
	if cdpURL == "" {
		return fmt.Errorf("E2E_CDP_URL is not set (run this suite through tests/e2e.sh)")
	}

	// PART 28 reserves ports 64000-64999 for end-to-end runs.
	port := envOr("E2E_PORT", "64123")
	httpBaseURL = "http://127.0.0.1:" + port
	browserBaseURL = "http://" + envOr("E2E_SELF_HOST", "127.0.0.1") + ":" + port

	base := filepath.Join(os.TempDir(), envOr("E2E_ORG", "apimgr"))
	if err := os.MkdirAll(base, 0o755); err != nil {
		return err
	}
	dir, err := os.MkdirTemp(base, envOr("E2E_NAME", "shortner")+"-e2e-")
	if err != nil {
		return err
	}
	artifactDir = dir

	cfgDir := filepath.Join(artifactDir, "config")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		return err
	}

	logFile, err := os.Create(filepath.Join(artifactDir, "server.log"))
	if err != nil {
		return err
	}

	serverCmd = exec.Command(bin,
		"--config", filepath.Join(cfgDir, "server.yml"),
		"--address", "0.0.0.0",
		"--port", port,
	)
	serverCmd.Stdout = logFile
	serverCmd.Stderr = logFile
	if err := serverCmd.Start(); err != nil {
		return fmt.Errorf("starting server: %w", err)
	}
	return waitForHealth(httpBaseURL+"/api/healthz", 60*time.Second)
}

func teardown() {
	if serverCmd != nil && serverCmd.Process != nil {
		_ = serverCmd.Process.Kill()
		_, _ = serverCmd.Process.Wait()
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// waitForHealth polls until the health endpoint answers 200 or the
// deadline passes.
func waitForHealth(url string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("server never became healthy at %s", url)
}

// browserContext returns a chromedp context attached to the remote browser
// plus a collector of every console error and uncaught exception seen on
// the page, which Tier 3 asserts is empty.
func browserContext(t *testing.T) (context.Context, *consoleErrors) {
	t.Helper()

	allocCtx, cancelAlloc := chromedp.NewRemoteAllocator(context.Background(), cdpURL)
	ctx, cancelCtx := chromedp.NewContext(allocCtx)
	timeoutCtx, cancelTimeout := context.WithTimeout(ctx, 60*time.Second)
	t.Cleanup(func() {
		cancelTimeout()
		cancelCtx()
		cancelAlloc()
	})

	errs := &consoleErrors{}
	chromedp.ListenTarget(timeoutCtx, func(ev any) {
		switch e := ev.(type) {
		case *runtime.EventConsoleAPICalled:
			if e.Type == runtime.APITypeError {
				errs.add(joinConsoleArgs(e.Args))
			}
		case *runtime.EventExceptionThrown:
			if e.ExceptionDetails != nil {
				errs.add(e.ExceptionDetails.Error())
			}
		}
	})
	return timeoutCtx, errs
}

// consoleErrors collects browser console errors seen during a test.
type consoleErrors struct {
	messages []string
}

func (c *consoleErrors) add(msg string) {
	c.messages = append(c.messages, msg)
}

func (c *consoleErrors) list() []string {
	return c.messages
}

func joinConsoleArgs(args []*runtime.RemoteObject) string {
	parts := make([]string, 0, len(args))
	for _, arg := range args {
		if arg == nil {
			continue
		}
		parts = append(parts, string(arg.Value))
	}
	return strings.Join(parts, " ")
}

// saveArtifacts writes the page HTML (and a screenshot when one was
// captured) into artifactDir so a CI failure is debuggable after the
// container is gone.
func saveArtifacts(t *testing.T, name, html string, screenshot []byte) {
	t.Helper()
	if artifactDir == "" {
		return
	}
	if html != "" {
		path := filepath.Join(artifactDir, name+".html")
		if err := os.WriteFile(path, []byte(html), 0o644); err == nil {
			t.Logf("saved page HTML to %s", path)
		}
	}
	if len(screenshot) > 0 {
		path := filepath.Join(artifactDir, name+".png")
		if err := os.WriteFile(path, screenshot, 0o644); err == nil {
			t.Logf("saved screenshot to %s", path)
		}
	}
}
