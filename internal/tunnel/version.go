package tunnel

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

const latestReleaseURL = "https://api.github.com/repos/cloudflare/cloudflared/releases/latest"

type VersionInfo struct {
	Installed       string `json:"installed,omitempty"`
	Latest          string `json:"latest,omitempty"`
	UpdateAvailable bool   `json:"update_available"`
}

type VersionChecker struct {
	mu        sync.Mutex
	latest    string
	nextCheck time.Time
}

func (c *VersionChecker) Check(ctx context.Context, binary string) VersionInfo {
	info := VersionInfo{Installed: installedVersion(ctx, binary)}
	installed, ok := parseVersion(info.Installed)
	if !ok {
		return info
	}
	info.Latest = c.latestVersion()
	if latest, ok := parseVersion(info.Latest); ok {
		info.UpdateAvailable = slices.Compare(installed[:], latest[:]) < 0
	}
	return info
}

func installedVersion(ctx context.Context, binary string) string {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "--version")
	cmd.WaitDelay = 250 * time.Millisecond
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	// Standard output is "cloudflared version 2026.10.7 (built ...)".
	fields := strings.Fields(string(output))
	if len(fields) >= 3 && fields[0] == "cloudflared" && fields[1] == "version" {
		return fields[2]
	}
	return ""
}

func (c *VersionChecker) latestVersion() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Now().Before(c.nextCheck) {
		return c.latest
	}

	// The shared cache should survive cancellation of one status request.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, latestReleaseURL, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	c.latest = ""
	c.nextCheck = time.Now().Add(time.Minute)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var release struct {
		Tag string `json:"tag_name"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&release) != nil {
		return ""
	}
	if _, ok := parseVersion(release.Tag); ok {
		c.latest = strings.TrimPrefix(release.Tag, "v")
		c.nextCheck = time.Now().Add(time.Hour)
	}
	return c.latest
}

func parseVersion(version string) ([3]uint64, bool) {
	var numbers [3]uint64
	parts := strings.Split(strings.TrimPrefix(version, "v"), ".")
	if len(parts) != len(numbers) {
		return numbers, false
	}
	for i, part := range parts {
		n, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return numbers, false
		}
		numbers[i] = n
	}
	return numbers, true
}
