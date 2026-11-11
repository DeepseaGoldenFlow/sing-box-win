package main

// Requirement C support: rule-set warm start via the upstream cache file.
// Basis: upstream sing-box v1.14.4 route/rule/rule_set_remote.go StartContext
// loads a cached rule-set before falling back to initial_path and finally a
// synchronous fetch. box.go only creates that cache file when the caller
// supplies experimental.cache_file (or a platform log writer), so SFW must
// supply it for rule-sets to survive a restart.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sagernet/sing-box/option"
)

func TestApplyRuleSetCacheEnablesUpstreamCacheFile(t *testing.T) {
	dir := t.TempDir()
	a := &App{dir: dir}
	opt := &option.Options{Route: &option.RouteOptions{RuleSet: []option.RuleSet{
		{Type: "remote", Tag: []string{"geo"}, Format: "binary", RemoteOptions: option.RemoteRuleSet{URL: "https://example.invalid/geo.srs"}},
		{Type: "local", Tag: []string{"local"}, Format: "source", LocalOptions: option.LocalRuleSet{Path: "local.json"}},
	}}}

	inventory := a.applyRuleSetCache(opt)
	if opt.Experimental == nil || opt.Experimental.CacheFile == nil || !opt.Experimental.CacheFile.Enabled {
		t.Fatal("upstream cache_file was not enabled")
	}
	want := filepath.Join(dir, "cache.db")
	if opt.Experimental.CacheFile.Path != want {
		t.Fatalf("cache path = %q, want %q", opt.Experimental.CacheFile.Path, want)
	}
	if inventory.remote != 1 || inventory.local != 1 {
		t.Fatalf("inventory = %+v", inventory)
	}
	if inventory.cacheFile != want || inventory.cacheReady {
		t.Fatalf("inventory cache = %+v", inventory)
	}
	if _, err := os.Stat(want); err == nil {
		t.Fatal("applyRuleSetCache must not create the database itself")
	}

	// A second call reports the file once the core has written it.
	if err := os.WriteFile(want, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if inventory = a.applyRuleSetCache(opt); !inventory.cacheReady {
		t.Fatal("existing cache file not detected")
	}
}

func TestApplyRuleSetCacheKeepsDeclaredPathAndSkipsLocalOnly(t *testing.T) {
	dir := t.TempDir()
	a := &App{dir: dir}
	opt := &option.Options{
		Experimental: &option.ExperimentalOptions{CacheFile: &option.CacheFileOptions{Path: "custom.db"}},
		Route: &option.RouteOptions{RuleSet: []option.RuleSet{
			{Type: "remote", Tag: []string{"geo"}, Format: "binary", RemoteOptions: option.RemoteRuleSet{URL: "https://example.invalid/geo.srs"}},
		}},
	}
	a.applyRuleSetCache(opt)
	if got := opt.Experimental.CacheFile.Path; got != filepath.Join(dir, "custom.db") {
		t.Fatalf("relative declared path not anchored to the data dir: %q", got)
	}

	local := &option.Options{Route: &option.RouteOptions{RuleSet: []option.RuleSet{
		{Type: "local", Tag: []string{"local"}, Format: "source", LocalOptions: option.LocalRuleSet{Path: "local.json"}},
	}}}
	if inventory := a.applyRuleSetCache(local); inventory.remote != 0 || local.Experimental != nil {
		t.Fatalf("local-only profile gained a cache file: %+v", local.Experimental)
	}
}
