package config

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestEndpointFidelity(t *testing.T) {
	path := os.Getenv("SINGBOX_TEST_SUB")
	if path == "" {
		t.Skip("set SINGBOX_TEST_SUB to downloaded subscription")
	}
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	o, e := Parse(b)
	if e != nil {
		t.Fatal(e)
	}
	if len(o.Endpoints) != 1 || o.Endpoints[0].Type != "tailscale" {
		t.Fatalf("endpoints: %v", o.Endpoints)
	}
	if o.Route == nil {
		t.Fatal("missing route")
	}
	rules, err := json.Marshal(o.Route.Rules)
	if err != nil {
		t.Fatal(err)
	}
	found := strings.Contains(string(rules), "100.64.0.0/10")
	if !found {
		t.Fatal("missing CIDR rule")
	}
	t.Log("len(opt.Endpoints)==1; type=tailscale; rule 100.64.0.0/10 exists=true")
}
