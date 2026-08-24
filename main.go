package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
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
	store        *cfgpkg.Store
	proc         *tunnelproc.Manager
	zoraxyPort   int
	zoraxyAPIKey string
}

type applyRequest struct {
	DeleteDNS []string `json:"delete_dns"`
}

type zoraxyProxyRule struct {
	RootOrMatchingDomain string   `json:"RootOrMatchingDomain"`
	MatchingDomainAlias  []string `json:"MatchingDomainAlias"`
	Disabled             bool     `json:"Disabled"`
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
		VersionPatch:  5,
		UIPath:        uiPath,
		PermittedAPIEndpoints: []plugin.PermittedAPIEndpoint{
			{
				Method:   http.MethodGet,
				Endpoint: "/plugin/api/proxy/list",
				Reason:   "Suggest hostnames from configured Zoraxy HTTP proxy rules",
			},
		},
	})
	if err != nil {
		panic(err)
	}

	store, err := cfgpkg.Load(cfgpkg.DefaultPath)
	if err != nil {
		panic(err)
	}
	app := &App{
		store:        store,
		proc:         &tunnelproc.Manager{},
		zoraxyPort:   runtimeCfg.ZoraxyPort,
		zoraxyAPIKey: runtimeCfg.APIKey,
	}

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
	mux.HandleFunc("/api/zoraxy/hostnames", app.handleZoraxyHostnames)
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
	var request applyRequest
	if r.Body != nil {
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		if err := decoder.Decode(&request); err != nil && !errors.Is(err, io.EOF) {
			fail(w, http.StatusBadRequest, fmt.Errorf("invalid apply request: %w", err))
			return
		}
	}
	client, cfg, err := a.cfClient()
	if err != nil {
		fail(w, 400, err)
		return
	}
	deleteDNS, err := normalizeDNSDeletions(request.DeleteDNS)
	if err != nil {
		fail(w, http.StatusBadRequest, err)
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
	active := make(map[string]bool, len(applied))
	for _, hostname := range applied {
		active[hostname] = true
	}
	deletedDNS := []string{}
	for _, hostname := range deleteDNS {
		if active[hostname] {
			continue
		}
		removed, err := client.DeleteCNAME(ctx, hostname)
		if err != nil {
			fail(w, 502, fmt.Errorf("delete DNS %s: %w", hostname, err))
			return
		}
		if removed {
			deletedDNS = append(deletedDNS, hostname)
		}
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

	result := map[string]any{"ok": true, "tunnel_id": t.ID, "tunnel_status": t.Status, "hostnames": applied, "deleted_dns": deletedDNS}
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

func normalizeDNSDeletions(rawHostnames []string) ([]string, error) {
	seen := map[string]bool{}
	hostnames := make([]string, 0, len(rawHostnames))
	for _, rawHostname := range rawHostnames {
		hostname := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(rawHostname), "."))
		if hostname == "" || seen[hostname] {
			continue
		}
		if !hostnameRE.MatchString(hostname) {
			return nil, fmt.Errorf("invalid hostname queued for DNS deletion: %q", rawHostname)
		}
		seen[hostname] = true
		hostnames = append(hostnames, hostname)
	}
	return hostnames, nil
}

func (a *App) handleZoraxyHostnames(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if a.zoraxyPort <= 0 || strings.TrimSpace(a.zoraxyAPIKey) == "" {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":        false,
			"error":     "Zoraxy did not provide plugin API access; hostname suggestions are unavailable",
			"hostnames": []string{},
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	endpoint := fmt.Sprintf("http://127.0.0.1:%d/plugin/api/proxy/list?type=host", a.zoraxyPort)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	req.Header.Set("Authorization", "Bearer "+a.zoraxyAPIKey)
	req.Header.Set("Accept", "application/json")
	resp, err := (&http.Client{Timeout: 8 * time.Second}).Do(req)
	if err != nil {
		fail(w, http.StatusBadGateway, fmt.Errorf("load Zoraxy HTTP rules: %w", err))
		return
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		fail(w, http.StatusBadGateway, err)
		return
	}
	if resp.StatusCode != http.StatusOK {
		fail(w, http.StatusBadGateway, fmt.Errorf("Zoraxy HTTP-rule API returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body))))
		return
	}
	var rules []zoraxyProxyRule
	if err := json.Unmarshal(body, &rules); err != nil {
		fail(w, http.StatusBadGateway, fmt.Errorf("decode Zoraxy HTTP rules: %w", err))
		return
	}

	hostnames := hostnamesFromZoraxyRules(rules)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "hostnames": hostnames})
}

func hostnamesFromZoraxyRules(rules []zoraxyProxyRule) []string {
	seen := map[string]bool{}
	hostnames := []string{}
	add := func(raw string) {
		hostname := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(raw), "."))
		if hostname == "" || seen[hostname] || !hostnameRE.MatchString(hostname) {
			return
		}
		seen[hostname] = true
		hostnames = append(hostnames, hostname)
	}
	for _, rule := range rules {
		if rule.Disabled {
			continue
		}
		add(rule.RootOrMatchingDomain)
		for _, alias := range rule.MatchingDomainAlias {
			add(alias)
		}
	}
	sort.Strings(hostnames)
	return hostnames
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
