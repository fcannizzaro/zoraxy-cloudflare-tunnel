package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/fcannizzaro/zoraxy-cloudflare-tunnel/internal/cloudflare"
	cfgpkg "github.com/fcannizzaro/zoraxy-cloudflare-tunnel/internal/config"
	tunnelproc "github.com/fcannizzaro/zoraxy-cloudflare-tunnel/internal/tunnel"
	plugin "github.com/fcannizzaro/zoraxy-cloudflare-tunnel/mod/zoraxy_plugin"
)

const (
	pluginID = "com.fcannizzaro.zoraxy-cloudflare-tunnel"
	uiPath   = "/"
	webRoot  = "/www"
)

//go:embed www/*
var webFS embed.FS

var hostnameRE = regexp.MustCompile(`^(\*\.)?([A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?\.)+[A-Za-z]{2,63}$`)

type App struct {
	store *cfgpkg.Store
	proc  *tunnelproc.Manager
}

func main() {
	runtimeCfg, err := plugin.ServeAndRecvSpec(&plugin.IntroSpect{
		ID:            pluginID,
		Name:          "Cloudflare Tunnel",
		Author:        "Francesco Saverio Cannizzaro",
		AuthorContact: "https://github.com/fcannizzaro",
		Description:   "Zoraxy plugin to automatically create and expose a hostname via Cloudflare Tunnel",
		URL:           "https://github.com/fcannizzaro/zoraxy-cloudflare-tunnel",
		Type:          plugin.PluginType_Utilities,
		VersionMajor:  1,
		VersionMinor:  0,
		VersionPatch:  4,
		UIPath:        uiPath,
	})
	if err != nil {
		panic(err)
	}

	store, err := cfgpkg.Load(cfgpkg.DefaultPath)
	if err != nil {
		panic(err)
	}
	app := &App{store: store, proc: &tunnelproc.Manager{}}

	mux := http.NewServeMux()
	router := plugin.NewPluginEmbedUIRouter(&webFS, webRoot, uiPath)
	if runtimeCfg.RuntimeConst.DevelopmentBuild {
		if st, err := os.Stat("./www"); err == nil && st.IsDir() {
			router.SetDevWebRoot("./www")
		}
	}
	router.RegisterTerminateHandler(func() { _ = app.proc.Stop() }, mux)

	mux.HandleFunc("/api/config", app.handleConfig)
	mux.HandleFunc("/api/apply", app.handleApply)
	mux.HandleFunc("/api/tunnel/start", app.handleStart)
	mux.HandleFunc("/api/tunnel/stop", app.handleStop)
	mux.HandleFunc("/api/status", app.handleStatus)
	mux.Handle("/", router.Handler())

	if store.Get().AutoStart {
		go func() {
			time.Sleep(500 * time.Millisecond)
			if err := app.startTunnel(context.Background()); err != nil {
				fmt.Println("[cloudflare-tunnel] autostart:", err)
			}
		}()
	}

	addr := "127.0.0.1:" + strconv.Itoa(runtimeCfg.Port)
	fmt.Println("[cloudflare-tunnel] UI listening on http://" + addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		panic(err)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]any{"ok": false, "error": err.Error()})
}

func (a *App) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg := a.store.Get()
		view := struct {
			cfgpkg.Config
			TokenConfigured bool `json:"token_configured"`
		}{Config: cfg, TokenConfigured: strings.TrimSpace(cfg.APIToken) != ""}
		view.APIToken = ""
		writeJSON(w, http.StatusOK, view)
	case http.MethodPost:
		var cfg cfgpkg.Config
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&cfg); err != nil {
			fail(w, 400, err)
			return
		}
		// The UI intentionally sends an empty token when the user wants to keep
		// the previously saved credential. Preserve it instead of clearing it.
		if strings.TrimSpace(cfg.APIToken) == "" {
			cfg.APIToken = a.store.Get().APIToken
		}
		if err := validateConfig(cfg, false); err != nil {
			fail(w, 400, err)
			return
		}
		if err := a.store.Replace(cfg); err != nil {
			fail(w, 500, err)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func validateConfig(cfg cfgpkg.Config, requireCreds bool) error {
	if requireCreds {
		if strings.TrimSpace(cfg.AccountID) == "" {
			return errors.New("Cloudflare Account ID is required")
		}
		if strings.TrimSpace(cfg.ZoneID) == "" {
			return errors.New("Cloudflare Zone ID is required")
		}
		if strings.TrimSpace(cfg.APIToken) == "" {
			return errors.New("Cloudflare API token is required")
		}
	}
	if strings.TrimSpace(cfg.TunnelName) == "" {
		return errors.New("tunnel name is required")
	}
	origin, err := url.Parse(cfg.OriginService)
	if err != nil || (origin.Scheme != "http" && origin.Scheme != "https") || origin.Host == "" {
		return errors.New("origin service must be an http:// or https:// URL")
	}
	seen := map[string]bool{}
	for _, route := range cfg.Routes {
		if !route.Enabled {
			continue
		}
		h := strings.ToLower(strings.TrimSpace(route.Hostname))
		if !hostnameRE.MatchString(h) {
			return fmt.Errorf("invalid hostname: %q", route.Hostname)
		}
		if seen[h] {
			return fmt.Errorf("duplicate hostname: %s", h)
		}
		seen[h] = true
	}
	return nil
}

func (a *App) cfClient() (*cloudflare.Client, cfgpkg.Config, error) {
	cfg := a.store.Get()
	if err := validateConfig(cfg, true); err != nil {
		return nil, cfg, err
	}
	return cloudflare.New(cfg.APIToken, cfg.AccountID, cfg.ZoneID), cfg, nil
}

func (a *App) handleApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(405)
		return
	}
	client, cfg, err := a.cfClient()
	if err != nil {
		fail(w, 400, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	t, err := client.EnsureTunnel(ctx, cfg.TunnelID, cfg.TunnelName)
	if err != nil {
		fail(w, 502, err)
		return
	}

	ingress := make([]cloudflare.IngressRule, 0, len(cfg.Routes)+1)
	applied := []string{}
	for _, route := range cfg.Routes {
		if !route.Enabled {
			continue
		}
		hostname := strings.ToLower(strings.TrimSpace(route.Hostname))
		originReq := map[string]any{}
		if cfg.MatchSNIToHost {
			originReq["matchSNItoHost"] = true
		}
		if cfg.NoTLSVerify {
			originReq["noTLSVerify"] = true
		}
		ingress = append(ingress, cloudflare.IngressRule{Hostname: hostname, Service: cfg.OriginService, OriginRequest: originReq})
		applied = append(applied, hostname)
	}
	ingress = append(ingress, cloudflare.IngressRule{Service: "http_status:404"})
	if err := client.PutIngress(ctx, t.ID, ingress); err != nil {
		fail(w, 502, err)
		return
	}
	for _, hostname := range applied {
		if err := client.UpsertTunnelDNS(ctx, hostname, t.ID); err != nil {
			fail(w, 502, fmt.Errorf("DNS %s: %w", hostname, err))
			return
		}
	}
	if err := a.store.Update(func(c *cfgpkg.Config) { c.TunnelID = t.ID }); err != nil {
		fail(w, 500, err)
		return
	}

	result := map[string]any{"ok": true, "tunnel_id": t.ID, "tunnel_status": t.Status, "hostnames": applied}
	// When AutoStart is enabled, Apply also brings the connector online. The
	// same setting is honored when the Zoraxy plugin process starts/restarts.
	if cfg.AutoStart && !a.proc.Status().Running {
		startCtx, startCancel := context.WithTimeout(context.Background(), 30*time.Second)
		startErr := a.startTunnel(startCtx)
		startCancel()
		if startErr != nil {
			result["autostart_error"] = startErr.Error()
		} else {
			result["auto_started"] = true
			result["process"] = a.proc.Status()
		}
	}
	writeJSON(w, 200, result)
}

func (a *App) startTunnel(ctx context.Context) error {
	client, cfg, err := a.cfClient()
	if err != nil {
		return err
	}
	if strings.TrimSpace(cfg.TunnelID) == "" {
		return errors.New("apply configuration first so a tunnel ID is available")
	}
	token, err := client.TunnelToken(ctx, cfg.TunnelID)
	if err != nil {
		return err
	}
	return a.proc.Start(cfg.CloudflaredPath, token)
}

func (a *App) handleStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(405)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := a.startTunnel(ctx); err != nil {
		fail(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "process": a.proc.Status()})
}

func (a *App) handleStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(405)
		return
	}
	if err := a.proc.Stop(); err != nil {
		fail(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (a *App) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(405)
		return
	}
	cfg := a.store.Get()
	resolvedBinary, binaryErr := tunnelproc.ResolveBinary(cfg.CloudflaredPath)
	result := map[string]any{
		"ok":                    true,
		"process":               a.proc.Status(),
		"tunnel_id":             cfg.TunnelID,
		"cloudflared_available": binaryErr == nil,
	}
	if binaryErr != nil {
		result["cloudflared_error"] = binaryErr.Error()
	} else {
		result["cloudflared_resolved_path"] = resolvedBinary
	}
	if cfg.TunnelID != "" && cfg.APIToken != "" && cfg.AccountID != "" {
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		c := cloudflare.New(cfg.APIToken, cfg.AccountID, cfg.ZoneID)
		if t, err := c.GetTunnel(ctx, cfg.TunnelID); err == nil {
			result["cloudflare_status"] = t.Status
		} else {
			result["cloudflare_error"] = err.Error()
		}
	}
	writeJSON(w, 200, result)
}
