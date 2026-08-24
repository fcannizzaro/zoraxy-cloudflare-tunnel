package zoraxy_plugin

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type PluginType int

const (
	PluginType_Utilities PluginType = 1
)

type RuntimeConstantValue struct {
	DevelopmentBuild bool `json:"development_build"`
}

type IntroSpect struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	Author        string     `json:"author"`
	AuthorContact string     `json:"author_contact"`
	Description   string     `json:"description"`
	URL           string     `json:"url"`
	Type          PluginType `json:"type"`
	VersionMajor  int        `json:"version_major"`
	VersionMinor  int        `json:"version_minor"`
	VersionPatch  int        `json:"version_patch"`
	UIPath        string     `json:"ui_path"`
}

type ConfigureSpec struct {
	Port         int                  `json:"port"`
	RuntimeConst RuntimeConstantValue `json:"runtime_const"`
}

func serveIntroSpect(spec *IntroSpect) {
	if len(os.Args) > 1 && os.Args[1] == "-introspect" {
		b, _ := json.MarshalIndent(spec, "", "  ")
		fmt.Println(string(b))
		os.Exit(0)
	}
}

func recvConfigureSpec() (*ConfigureSpec, error) {
	for i, arg := range os.Args {
		var raw string
		switch {
		case strings.HasPrefix(arg, "-configure="):
			raw = strings.TrimPrefix(arg, "-configure=")
		case arg == "-configure" && i+1 < len(os.Args):
			raw = os.Args[i+1]
		}
		if raw == "" {
			continue
		}
		var cfg ConfigureSpec
		if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
			return nil, fmt.Errorf("parse Zoraxy configure spec: %w", err)
		}
		if cfg.Port <= 0 {
			return nil, fmt.Errorf("invalid Zoraxy-assigned plugin port: %d", cfg.Port)
		}
		return &cfg, nil
	}
	return nil, fmt.Errorf("no -configure flag found")
}

func ServeAndRecvSpec(spec *IntroSpect) (*ConfigureSpec, error) {
	serveIntroSpect(spec)
	return recvConfigureSpec()
}
