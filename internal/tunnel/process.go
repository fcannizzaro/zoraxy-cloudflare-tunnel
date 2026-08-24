package tunnel

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

type Manager struct {
	mu        sync.Mutex
	cmd       *exec.Cmd
	startedAt time.Time
	lastExit  string
}

type Status struct {
	Running   bool   `json:"running"`
	PID       int    `json:"pid,omitempty"`
	StartedAt string `json:"started_at,omitempty"`
	LastExit  string `json:"last_exit,omitempty"`
}

// ResolveBinary finds cloudflared using the configured value first and, when
// the default name is used, a short list of common installation locations.
// This helps when Zoraxy is started by systemd with a more restricted PATH.
func ResolveBinary(binary string) (string, error) {
	binary = strings.TrimSpace(binary)
	if binary == "" {
		binary = "cloudflared"
	}

	if resolved, err := exec.LookPath(binary); err == nil {
		return resolved, nil
	}

	// An explicit path should not silently fall back to some other binary.
	if strings.ContainsRune(binary, os.PathSeparator) || filepath.IsAbs(binary) {
		return "", fmt.Errorf("cloudflared executable not found at %s", binary)
	}

	if binary == "cloudflared" && runtime.GOOS != "windows" {
		for _, candidate := range []string{
			"/usr/local/bin/cloudflared",
			"/usr/bin/cloudflared",
			"/usr/local/sbin/cloudflared",
			"/usr/sbin/cloudflared",
			"/opt/cloudflared/cloudflared",
		} {
			info, err := os.Stat(candidate)
			if err == nil && !info.IsDir() && info.Mode()&0111 != 0 {
				return candidate, nil
			}
		}
	}

	return "", fmt.Errorf("cloudflared executable not found (%s). Install cloudflared in the Zoraxy host/LXC or set its full path in the plugin", binary)
}

func (m *Manager) Start(binary, token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cmd != nil && m.cmd.Process != nil && m.cmd.ProcessState == nil {
		return errors.New("cloudflared is already running")
	}
	if token == "" {
		return errors.New("empty Cloudflare tunnel token")
	}

	resolved, err := ResolveBinary(binary)
	if err != nil {
		return err
	}
	cmd := exec.Command(resolved, "tunnel", "--no-autoupdate", "run")
	cmd.Env = append(os.Environ(), "TUNNEL_TOKEN="+token)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	m.cmd = cmd
	m.startedAt = time.Now()
	m.lastExit = ""
	go func(c *exec.Cmd) {
		err := c.Wait()
		m.mu.Lock()
		defer m.mu.Unlock()
		if err != nil {
			m.lastExit = err.Error()
		} else {
			m.lastExit = "exited normally"
		}
		if m.cmd == c {
			m.cmd = nil
		}
	}(cmd)
	return nil
}

func (m *Manager) Stop() error {
	m.mu.Lock()
	cmd := m.cmd
	m.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		_ = cmd.Process.Kill()
	}
	return nil
}

func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := Status{LastExit: m.lastExit}
	if m.cmd != nil && m.cmd.Process != nil && m.cmd.ProcessState == nil {
		s.Running = true
		s.PID = m.cmd.Process.Pid
		s.StartedAt = m.startedAt.Format(time.RFC3339)
	}
	return s
}
