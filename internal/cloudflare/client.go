package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const BaseURL = "https://api.cloudflare.com/client/v4"

type Client struct {
	Token     string
	AccountID string
	ZoneID    string
	HTTP      *http.Client
}

type apiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}
type envelope struct {
	Success bool            `json:"success"`
	Errors  []apiError      `json:"errors"`
	Result  json.RawMessage `json:"result"`
}

type Tunnel struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	ConfigSrc string `json:"config_src"`
}

type DNSRecord struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	Proxied bool   `json:"proxied"`
}

type IngressRule struct {
	Hostname      string         `json:"hostname,omitempty"`
	Service       string         `json:"service"`
	OriginRequest map[string]any `json:"originRequest,omitempty"`
}

func New(token, accountID, zoneID string) *Client {
	return &Client{Token: strings.TrimSpace(token), AccountID: strings.TrimSpace(accountID), ZoneID: strings.TrimSpace(zoneID), HTTP: &http.Client{Timeout: 30 * time.Second}}
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, BaseURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	var env envelope
	if err := json.Unmarshal(payload, &env); err != nil {
		return fmt.Errorf("Cloudflare API returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(payload)))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || !env.Success {
		msgs := make([]string, 0, len(env.Errors))
		for _, e := range env.Errors {
			msgs = append(msgs, fmt.Sprintf("%d: %s", e.Code, e.Message))
		}
		if len(msgs) == 0 {
			msgs = append(msgs, resp.Status)
		}
		return fmt.Errorf("Cloudflare API: %s", strings.Join(msgs, "; "))
	}
	if out != nil && len(env.Result) > 0 && string(env.Result) != "null" {
		if err := json.Unmarshal(env.Result, out); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) FindTunnelByName(ctx context.Context, name string) (*Tunnel, error) {
	q := url.Values{}
	q.Set("name", name)
	q.Set("is_deleted", "false")
	q.Set("per_page", "100")
	var tunnels []Tunnel
	if err := c.do(ctx, http.MethodGet, "/accounts/"+url.PathEscape(c.AccountID)+"/cfd_tunnel?"+q.Encode(), nil, &tunnels); err != nil {
		return nil, err
	}
	for _, t := range tunnels {
		if t.Name == name {
			x := t
			return &x, nil
		}
	}
	return nil, nil
}

func (c *Client) GetTunnel(ctx context.Context, id string) (*Tunnel, error) {
	var t Tunnel
	if err := c.do(ctx, http.MethodGet, "/accounts/"+url.PathEscape(c.AccountID)+"/cfd_tunnel/"+url.PathEscape(id), nil, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

func (c *Client) CreateTunnel(ctx context.Context, name string) (*Tunnel, error) {
	var t Tunnel
	body := map[string]any{"name": name, "config_src": "cloudflare"}
	if err := c.do(ctx, http.MethodPost, "/accounts/"+url.PathEscape(c.AccountID)+"/cfd_tunnel", body, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

func (c *Client) EnsureTunnel(ctx context.Context, id, name string) (*Tunnel, error) {
	if strings.TrimSpace(id) != "" {
		t, err := c.GetTunnel(ctx, id)
		if err == nil {
			return t, nil
		}
	}
	t, err := c.FindTunnelByName(ctx, name)
	if err != nil {
		return nil, err
	}
	if t != nil {
		return t, nil
	}
	return c.CreateTunnel(ctx, name)
}

func (c *Client) PutIngress(ctx context.Context, tunnelID string, ingress []IngressRule) error {
	body := map[string]any{"config": map[string]any{"ingress": ingress}}
	return c.do(ctx, http.MethodPut, "/accounts/"+url.PathEscape(c.AccountID)+"/cfd_tunnel/"+url.PathEscape(tunnelID)+"/configurations", body, nil)
}

func (c *Client) TunnelToken(ctx context.Context, tunnelID string) (string, error) {
	var token string
	if err := c.do(ctx, http.MethodGet, "/accounts/"+url.PathEscape(c.AccountID)+"/cfd_tunnel/"+url.PathEscape(tunnelID)+"/token", nil, &token); err != nil {
		return "", err
	}
	return token, nil
}

func (c *Client) findDNS(ctx context.Context, hostname string) (*DNSRecord, error) {
	q := url.Values{}
	q.Set("type", "CNAME")
	q.Set("name", hostname)
	q.Set("per_page", "100")
	var records []DNSRecord
	if err := c.do(ctx, http.MethodGet, "/zones/"+url.PathEscape(c.ZoneID)+"/dns_records?"+q.Encode(), nil, &records); err != nil {
		return nil, err
	}
	for _, r := range records {
		if strings.EqualFold(strings.TrimSuffix(r.Name, "."), strings.TrimSuffix(hostname, ".")) {
			x := r
			return &x, nil
		}
	}
	return nil, nil
}

func (c *Client) UpsertTunnelDNS(ctx context.Context, hostname, tunnelID string) error {
	desired := map[string]any{"type": "CNAME", "name": hostname, "content": tunnelID + ".cfargotunnel.com", "proxied": true}
	current, err := c.findDNS(ctx, hostname)
	if err != nil {
		return err
	}
	if current == nil {
		return c.do(ctx, http.MethodPost, "/zones/"+url.PathEscape(c.ZoneID)+"/dns_records", desired, nil)
	}
	return c.do(ctx, http.MethodPut, "/zones/"+url.PathEscape(c.ZoneID)+"/dns_records/"+url.PathEscape(current.ID), desired, nil)
}
