package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

type profileRouting struct {
	Outbounds []struct {
		Tag  string `json:"tag"`
		Type string `json:"type"`
	} `json:"outbounds"`
	Route struct {
		Final string            `json:"final"`
		Rules []json.RawMessage `json:"rules"`
	} `json:"route"`
}

func (a *App) profileRouting() (profileRouting, error) {
	var result profileRouting
	for _, p := range a.settings.Profiles {
		if p.Enabled {
			b, err := os.ReadFile(a.profilePath(p.ID))
			if err != nil {
				return result, err
			}
			err = json.Unmarshal(b, &result)
			return result, err
		}
	}
	return result, errors.New("no active profile")
}
func configuredGroupOrder(config profileRouting) []string {
	tags := []string{}
	for _, out := range config.Outbounds {
		switch out.Type {
		case "selector", "urltest", "fallback", "loadbalance", "relay":
			tags = append(tags, out.Tag)
		}
	}
	return tags
}
func (a *App) routingInfo() (any, error) {
	profile, err := a.profileRouting()
	if err != nil {
		return nil, err
	}
	value, err := a.clashCall("GET", "/configs")
	if err != nil {
		return nil, err
	}
	root, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("invalid routing mode response")
	}
	mode, _ := root["mode"].(string)
	return map[string]any{"mode": mode, "available_modes": root["mode-list"], "rules_count": len(profile.Route.Rules), "final": profile.Route.Final}, nil
}

func (a *App) setRoutingMode(mode string) error {
	if a.core == nil {
		return errors.New("core is stopped")
	}
	value, err := a.clashCall("GET", "/configs")
	if err != nil {
		return err
	}
	root, ok := value.(map[string]any)
	if !ok {
		return errors.New("invalid mode response")
	}
	allowed := false
	if modes, ok := root["mode-list"].([]any); ok {
		for _, candidate := range modes {
			if name, ok := candidate.(string); ok && strings.EqualFold(name, mode) {
				mode = name
				allowed = true
			}
		}
	}
	if !allowed {
		return errors.New("mode is not advertised by this profile")
	}
	data, _ := json.Marshal(map[string]string{"mode": mode})
	req, err := http.NewRequest("PATCH", a.clash+"/configs", bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+a.secret)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("Clash mode update HTTP %d", resp.StatusCode)
	}
	return nil
}
