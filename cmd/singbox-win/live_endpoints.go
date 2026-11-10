package main

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/sagernet/sing-box/adapter"
	"os"
	"time"
)

type endpointSnapshot struct {
	Status  *adapter.TailscaleEndpointStatus
	Updated time.Time
	Error   string
}

func (a *App) stopEndpointStatus() {
	if a.endpointCancel != nil {
		a.endpointCancel()
		a.endpointCancel = nil
	}
	a.endpointMu.Lock()
	a.endpointStates = nil
	a.endpointMu.Unlock()
}
func (a *App) startEndpointStatus() {
	a.stopEndpointStatus()
	ctx, cancel := context.WithCancel(context.Background())
	a.endpointCancel = cancel
	a.endpointMu.Lock()
	a.endpointStates = map[string]endpointSnapshot{}
	a.endpointMu.Unlock()
	for _, ep := range a.core.Endpoint().Endpoints() {
		tail, ok := ep.(adapter.TailscaleEndpoint)
		if !ok {
			continue
		}
		tag := ep.Tag()
		go func() {
			err := tail.SubscribeTailscaleStatus(ctx, func(status *adapter.TailscaleEndpointStatus) {
				a.endpointMu.Lock()
				defer a.endpointMu.Unlock()
				if ctx.Err() == nil {
					a.endpointStates[tag] = endpointSnapshot{Status: status, Updated: time.Now()}
				}
			})
			if err != nil && ctx.Err() == nil {
				a.endpointMu.Lock()
				defer a.endpointMu.Unlock()
				if ctx.Err() == nil {
					snapshot := a.endpointStates[tag]
					snapshot.Error = err.Error()
					a.endpointStates[tag] = snapshot
				}
			}
		}()
	}
}
func publicPeer(peer *adapter.TailscalePeer) any {
	if peer == nil {
		return nil
	}
	return map[string]any{"id": peer.StableID, "hostname": peer.HostName, "dns_name": peer.DNSName, "ips": peer.TailscaleIPs,
		"online": peer.Online, "expired": peer.Expired, "os": peer.OS, "exit_node": peer.ExitNode, "exit_node_option": peer.ExitNodeOption,
		"key_expiry": peer.KeyExpiry, "last_seen": peer.LastSeen}
}
func (a *App) liveEndpoints() ([]map[string]any, error) {
	var configured struct {
		Endpoints []struct {
			Tag  string `json:"tag"`
			Type string `json:"type"`
		} `json:"endpoints"`
	}
	for _, p := range a.settings.Profiles {
		if p.Enabled {
			b, err := os.ReadFile(a.profilePath(p.ID))
			if err != nil {
				return nil, err
			}
			if err = json.Unmarshal(b, &configured); err != nil {
				return nil, err
			}
			break
		}
	}
	out := []map[string]any{}
	a.endpointMu.RLock()
	defer a.endpointMu.RUnlock()
	for _, ep := range configured.Endpoints {
		item := map[string]any{"tag": ep.Tag, "type": ep.Type, "source": "unavailable", "backend_state": "Stopped"}
		if a.core != nil {
			item["backend_state"] = "Connecting"
		}
		if snapshot, ok := a.endpointStates[ep.Tag]; ok {
			item["error"] = snapshot.Error
			if status := snapshot.Status; status != nil {
				peers := []any{}
				for _, group := range status.UserGroups {
					for _, peer := range group.Peers {
						peers = append(peers, publicPeer(peer))
					}
				}
				item["source"] = "live"
				item["backend_state"] = status.BackendState
				item["self"] = publicPeer(status.Self)
				item["peers"] = peers
				item["exit_node"] = publicPeer(status.ExitNode)
				item["needs_login"] = status.AuthURL != ""
				item["last_sync"] = snapshot.Updated.Format(time.RFC3339)
			}
		}
		out = append(out, item)
	}
	return out, nil
}
func (a *App) setEndpointExit(tag, id string) error {
	if a.core == nil {
		return errors.New("core is stopped")
	}
	ep, ok := a.core.Endpoint().Get(tag)
	if !ok {
		return errors.New("endpoint not found")
	}
	tail, ok := ep.(adapter.TailscaleEndpoint)
	if !ok {
		return errors.New("endpoint has no Tailscale controls")
	}
	if id != "" {
		allowed := false
		a.endpointMu.RLock()
		if snapshot, ok := a.endpointStates[tag]; ok && snapshot.Status != nil {
			for _, group := range snapshot.Status.UserGroups {
				for _, peer := range group.Peers {
					if peer.StableID == id && peer.ExitNodeOption && !peer.Expired {
						allowed = true
					}
				}
			}
		}
		a.endpointMu.RUnlock()
		if !allowed {
			return errors.New("peer is not an available exit node")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return tail.SetTailscaleExitNode(ctx, id)
}
