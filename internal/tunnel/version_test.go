package tunnel

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type releaseTransport func(*http.Request) (*http.Response, error)

func (f releaseTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestVersionChecker(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("version fixture uses a shell script")
	}
	binary := filepath.Join(t.TempDir(), "custom cloudflared")
	script := `#!/bin/sh
[ "$1" = --version ] || exit 1
printf 'cloudflared version %s (built test)\n' "$TEST_CLOUDFLARED_VERSION"
`
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}

	statusCode := http.StatusOK
	requests := 0
	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	http.DefaultClient = &http.Client{Transport: releaseTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		if r.Method != http.MethodGet || r.URL.String() != latestReleaseURL {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL)
		}
		return &http.Response{
			StatusCode: statusCode,
			Body:       io.NopCloser(strings.NewReader(`{"tag_name":"v2026.10.2"}`)),
		}, nil
	})}

	var checker VersionChecker
	for _, tc := range []struct {
		installed string
		update    bool
	}{
		{"2026.9.12", true},
		{"2026.10.2", false},
		{"2026.10.12", false},
	} {
		t.Setenv("TEST_CLOUDFLARED_VERSION", tc.installed)
		want := VersionInfo{Installed: tc.installed, Latest: "2026.10.2", UpdateAvailable: tc.update}
		if got := checker.Check(context.Background(), binary); got != want {
			t.Errorf("Check() = %+v, want %+v", got, want)
		}
	}
	if requests != 1 {
		t.Fatalf("latest release fetched %d times, want 1", requests)
	}

	checker.nextCheck = time.Time{}
	statusCode = http.StatusServiceUnavailable
	t.Setenv("TEST_CLOUDFLARED_VERSION", "2026.9.12")
	for range 2 {
		want := VersionInfo{Installed: "2026.9.12"}
		if got := checker.Check(context.Background(), binary); got != want {
			t.Errorf("Check() when GitHub is unavailable = %+v, want %+v", got, want)
		}
	}
	if requests != 2 {
		t.Errorf("latest release fetched %d times after failed refresh, want 2", requests)
	}
}
