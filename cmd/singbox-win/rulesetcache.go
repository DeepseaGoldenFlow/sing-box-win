package main

// Rule-set warm start.
//
// Basis: upstream sing-box v1.14.4 already persists remotely fetched rule-sets
// in its global cache file and restores them on the next start, only fetching
// when nothing usable is stored (route/rule/rule_set_remote.go StartContext:
// cacheFile.LoadRuleSet -> initial_path -> fetch). That cache file only exists
// when the caller supplies experimental.cache_file (box.go: needCacheFile), and
// without it the fetch happens synchronously inside the Start stage, where a
// failure aborts startup (route/router.go Start: task.Group FastFail).
//
// SFW therefore enables the upstream cache file inside the data directory for
// every profile. It adds no new caching semantics: the same upstream component
// stores and restores the rule-sets. A first run that still has to fetch is
// covered by requirement C — core startup runs off the request path
// (startAsync), so a download cannot delay the window or the API.

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sagernet/sing-box/option"
)

// ruleSetInventory summarises the rule-sets a profile configures. It is filled
// while options are prepared and reported once the core reports them ready.
type ruleSetInventory struct {
	local      int
	remote     int
	cacheFile  string
	cacheReady bool
	remoteTags []string
}

// applyRuleSetCache enables the upstream cache file and returns the inventory.
//
// Every configured rule-set is resolved during the core's Start stage
// (route/router.go StartStateStart loads them, then box.Start logs
// "sing-box started"), so this stage is also the point to tie the readiness
// marks to.
func (a *App) applyRuleSetCache(opt *option.Options) ruleSetInventory {
	var inventory ruleSetInventory
	if opt.Route == nil || len(opt.Route.RuleSet) == 0 {
		return inventory
	}
	for _, ruleSet := range opt.Route.RuleSet {
		if ruleSet.Type == "remote" {
			inventory.remote++
			inventory.remoteTags = append(inventory.remoteTags, strings.Join(ruleSet.Tag, ","))
			continue
		}
		inventory.local++
	}
	// Local rule-sets need no cache and are resolved from disk in Start, so the
	// cache file is only touched when there is something to keep warm.
	if inventory.remote == 0 {
		return inventory
	}
	if opt.Experimental == nil {
		opt.Experimental = &option.ExperimentalOptions{}
	}
	if opt.Experimental.CacheFile == nil {
		opt.Experimental.CacheFile = &option.CacheFileOptions{}
	}
	cache := opt.Experimental.CacheFile
	// Platform override: SFW's data directory is the only writable location the
	// user manages, so the cache lives beside settings.json rather than in the
	// process working directory. Any path the profile already declared is kept.
	cache.Enabled = true
	if cache.Path == "" {
		cache.Path = filepath.Join(a.dir, "cache.db")
	} else if !filepath.IsAbs(cache.Path) {
		cache.Path = filepath.Join(a.dir, cache.Path)
	}
	inventory.cacheFile = cache.Path
	inventory.cacheReady = fileExists(cache.Path)
	return inventory
}

func (inventory ruleSetInventory) markReadyAt(r *startupRecorder, at time.Time) {
	r.MarkAt(nodeRuleSetLocal, at, map[string]any{"count": inventory.local})
	remoteFields := map[string]any{
		"count":         inventory.remote,
		"cache_file":    inventory.cacheFile,
		"cache_present": inventory.cacheReady,
	}
	if len(inventory.remoteTags) > 0 {
		remoteFields["tags"] = strings.Join(inventory.remoteTags, ",")
	}
	r.MarkAt(nodeRuleSetRemote, at, remoteFields)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
