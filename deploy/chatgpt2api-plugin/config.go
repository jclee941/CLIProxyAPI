package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/url"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type modelNames []string

func (names *modelNames) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.SequenceNode {
		return failure(400, "invalid_plugin_config")
	}
	parsed := make(modelNames, 0, len(node.Content))
	seen := make(map[string]struct{}, len(node.Content))
	for _, item := range node.Content {
		name := strings.TrimSpace(item.Value)
		if item.Kind != yaml.ScalarNode || item.Tag != "!!str" || name == "" {
			return failure(400, "invalid_plugin_config")
		}
		switch strings.ToLower(name) {
		case "auto", "gpt-image-2", "universalauto":
			return failure(400, "invalid_plugin_config")
		}
		if _, duplicate := seen[name]; duplicate {
			return failure(400, "invalid_plugin_config")
		}
		seen[name] = struct{}{}
		parsed = append(parsed, name)
	}
	*names = parsed
	return nil
}

type pluginConfig struct {
	ModelNames    modelNames `yaml:"model_names"`
	DashboardPath string     `yaml:"dashboard_path"`
	APIBaseURL    string     `yaml:"api_base_url"`
	Enabled       *bool      `yaml:"enabled"`
	Priority      *int       `yaml:"priority"`
}

const defaultAPIBaseURL = "http://chatgpt2api:80"

func validAPIBaseURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || parsed.RawPath != "" || (parsed.Path != "" && parsed.Path != "/") {
		return false
	}
	if parsed.Scheme == "https" {
		return true
	}
	address := net.ParseIP(parsed.Hostname())
	return parsed.Scheme == "http" && (raw == defaultAPIBaseURL || address != nil && address.IsLoopback())
}

func (plugin *service) register(raw []byte) (json.RawMessage, *publicError) {
	var request struct {
		ConfigYAML []byte `json:"config_yaml"`
	}
	if json.Unmarshal(raw, &request) != nil {
		return nil, failure(400, "invalid_registration")
	}
	config := pluginConfig{DashboardPath: defaultDashboardPath, APIBaseURL: defaultAPIBaseURL}
	decoder := yaml.NewDecoder(bytes.NewReader(request.ConfigYAML))
	decoder.KnownFields(true)
	if decoder.Decode(&config) != nil || decoder.Decode(new(yaml.Node)) != io.EOF || config.ModelNames == nil || !filepath.IsAbs(config.DashboardPath) || filepath.Ext(config.DashboardPath) != ".html" || !validAPIBaseURL(config.APIBaseURL) {
		return nil, failure(400, "invalid_plugin_config")
	}
	plugin.mu.Lock()
	plugin.config = config
	plugin.mu.Unlock()
	return json.RawMessage(`{"schema_version":6,"metadata":{"Name":"chatgpt2api","Version":"1.0.1","Author":"jclee941","GitHubRepository":"https://github.com/jclee941/CLIProxyAPI","ConfigFields":[{"Name":"model_names","Type":"array","Description":"Explicit exact model allowlist; empty declines all routes. Shared auto and gpt-image-2 are forbidden."},{"Name":"dashboard_path","Type":"string","Description":"Trusted absolute static dashboard HTML path"},{"Name":"api_base_url","Type":"string","Description":"Trusted private Web account API origin; default http://chatgpt2api:80. Credentials come only from runtime CHATGPT2API_AUTH_KEY."}]},"capabilities":{"model_router":true,"management_api":true}}`), nil
}
