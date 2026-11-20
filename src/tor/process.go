package tor

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cretz/bine/process"
)

type torProcessCreator struct {
	binary  string
	timeout time.Duration
	mu      sync.Mutex
	file    string
}

func (c *torProcessCreator) New(ctx context.Context, args ...string) (process.Process, error) {
	controlFile := ""
	for index := 0; index+1 < len(args); index++ {
		arg := args[index]
		if arg == "--ControlPortWriteToFile" {
			controlFile = args[index+1]
			break
		}
	}
	if controlFile == "" {
		return nil, fmt.Errorf("tor: Bine did not configure a control-port file")
	}
	c.mu.Lock()
	c.file = controlFile
	c.mu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	output := &lockedBuffer{}
	cmd := exec.CommandContext(ctx, c.binary, args...)
	cmd.Stdout = output
	cmd.Stderr = output
	return &torProcess{cmd: cmd, controlFile: controlFile, timeout: c.timeout, output: output, done: make(chan struct{})}, nil
}

func (c *torProcessCreator) removeControlPortFile() {
	c.mu.Lock()
	path := c.file
	c.file = ""
	c.mu.Unlock()
	if path != "" {
		_ = os.Remove(path)
	}
}

type torProcess struct {
	cmd         *exec.Cmd
	controlFile string
	timeout     time.Duration
	output      *lockedBuffer
	done        chan struct{}
	waitErr     error
}

func (p *torProcess) Start() error {
	if err := p.cmd.Start(); err != nil {
		return fmt.Errorf("start tor: %w; stderr: %s", err, p.output.String())
	}
	go func() {
		p.waitErr = p.cmd.Wait()
		close(p.done)
	}()
	deadline := time.NewTimer(p.timeout)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer deadline.Stop()
	defer ticker.Stop()
	for {
		if controlPortReady(p.controlFile) {
			return nil
		}
		select {
		case <-p.done:
			return fmt.Errorf("tor exited before the control port was ready: %v; stderr: %s", p.waitErr, p.output.String())
		case <-deadline.C:
			p.stopAfterStartFailure()
			return fmt.Errorf("timed out after %s waiting for the Tor control port; stderr: %s", p.timeout, p.output.String())
		case <-ticker.C:
		}
	}
}

func (p *torProcess) stopAfterStartFailure() {
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
		<-p.done
	}
}

func (p *torProcess) Wait() error {
	<-p.done
	return p.waitErr
}

func (*torProcess) EmbeddedControlConn() (net.Conn, error) {
	return nil, process.ErrControlConnUnsupported
}

func controlPortReady(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	value := string(data)
	if !strings.HasPrefix(value, "PORT=127.0.0.1:") || !strings.HasSuffix(value, "\n") {
		return false
	}
	address := strings.TrimSuffix(strings.TrimPrefix(value, "PORT="), "\n")
	host, portText, err := net.SplitHostPort(address)
	if err != nil || host != "127.0.0.1" {
		return false
	}
	port, err := strconv.Atoi(portText)
	return err == nil && port >= 1 && port <= 65535
}

type lockedBuffer struct {
	mu sync.Mutex
	bytes.Buffer
}

func (b *lockedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.Write(data)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.String()
}
