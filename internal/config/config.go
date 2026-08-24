package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const DefaultPath = "cloudflare-tunnel.json"

type Route struct {
	Hostname string `json:"hostname"`
	Enabled  bool   `json:"enabled"`
}

type Config struct {
	AccountID       string  `json:"account_id"`
	ZoneID          string  `json:"zone_id"`
	APIToken        string  `json:"api_token"`
	TunnelID        string  `json:"tunnel_id"`
	TunnelName      string  `json:"tunnel_name"`
	CloudflaredPath string  `json:"cloudflared_path"`
	OriginService   string  `json:"origin_service"`
	MatchSNIToHost  bool    `json:"match_sni_to_host"`
	NoTLSVerify     bool    `json:"no_tls_verify"`
	AutoStart       bool    `json:"auto_start"`
	Routes          []Route `json:"routes"`
}

func Defaults() Config {
	return Config{
		TunnelName:      "zoraxy",
		CloudflaredPath: "cloudflared",
		OriginService:   "https://127.0.0.1:443",
		MatchSNIToHost:  true,
		Routes:          []Route{},
	}
}

type Store struct {
	mu   sync.RWMutex
	path string
	cfg  Config
}

func Load(path string) (*Store, error) {
	if path == "" {
		path = DefaultPath
	}
	s := &Store{path: path, cfg: Defaults()}
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(b, &s.cfg); err != nil {
		return nil, err
	}
	s.normalize()
	return s, nil
}

func (s *Store) normalize() {
	if strings.TrimSpace(s.cfg.TunnelName) == "" {
		s.cfg.TunnelName = "zoraxy"
	}
	if strings.TrimSpace(s.cfg.CloudflaredPath) == "" {
		s.cfg.CloudflaredPath = "cloudflared"
	}
	if strings.TrimSpace(s.cfg.OriginService) == "" {
		s.cfg.OriginService = "https://127.0.0.1:443"
	}
	if s.cfg.Routes == nil {
		s.cfg.Routes = []Route{}
	}
}

func (s *Store) Get() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := s.cfg
	out.Routes = append([]Route(nil), s.cfg.Routes...)
	return out
}

func (s *Store) Update(fn func(*Config)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.cfg)
	s.normalize()
	return s.saveLocked()
}

func (s *Store) Replace(cfg Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(cfg.APIToken) == "" {
		cfg.APIToken = s.cfg.APIToken
	}
	s.cfg = cfg
	s.normalize()
	return s.saveLocked()
}

func (s *Store) saveLocked() error {
	b, err := json.MarshalIndent(s.cfg, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	if dir != "." {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
