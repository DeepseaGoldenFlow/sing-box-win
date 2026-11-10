package main

import (
	"encoding/json"
	"errors"
	"os"
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
	return map[string]any{"mode": mode, "rules_count": len(profile.Route.Rules), "final": profile.Route.Final}, nil
}
