package broker

import (
	"strings"
	"testing"
	"time"
)

const testInstance = "0123456789abcdef0123456789abcdef"

func TestRequestWhitelist(t *testing.T) {
	base := Request{Version: ProtocolVersion, Action: ActionPing, InstanceID: testInstance}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid ping rejected: %v", err)
	}
	for _, action := range []string{"start", "stop", "reload", "run", "tun/start", "", "PING"} {
		bad := base
		bad.Action = action
		if err := bad.Validate(); err == nil {
			t.Fatalf("action %q accepted", action)
		}
	}
	wrongVersion := base
	wrongVersion.Version = 2
	if err := wrongVersion.Validate(); err == nil {
		t.Fatal("unknown protocol version accepted")
	}
	badInstance := base
	badInstance.InstanceID = "../../etc"
	if err := badInstance.Validate(); err == nil {
		t.Fatal("path-like instance id accepted")
	}
}

func TestRecoverIsDryRunOnly(t *testing.T) {
	recover := Request{Version: ProtocolVersion, Action: ActionRecover, InstanceID: testInstance}
	if err := recover.Validate(); err != nil {
		t.Fatalf("dry-run recover rejected: %v", err)
	}
	yes := true
	recover.DryRun = &yes
	if err := recover.Validate(); err != nil {
		t.Fatalf("explicit dry-run rejected: %v", err)
	}
	no := false
	recover.DryRun = &no
	if err := recover.Validate(); err == nil {
		t.Fatal("real recover accepted in P0")
	}
	if len(RecoverPlan()) == 0 {
		t.Fatal("recover plan is empty")
	}
	for _, line := range RecoverPlan() {
		if !strings.HasPrefix(line, "dry-run:") {
			t.Fatalf("recover plan promises work: %q", line)
		}
	}
}

func TestCapabilityBinding(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	now := time.Unix(1_800_000_000, 0)
	caller := CallerIdentity{UserSID: "S-1-5-21-1", SessionID: 3}
	token, expires, err := IssueCapability(key, caller, testInstance, 7, now)
	if err != nil {
		t.Fatal(err)
	}
	if !expires.After(now) {
		t.Fatal("capability does not expire in the future")
	}
	claims, err := Verify(key, token, caller, testInstance, 7, now.Add(time.Minute))
	if err != nil || claims.Generation != 7 {
		t.Fatalf("verify: %+v %v", claims, err)
	}

	// Every binding must be enforced.
	cases := []struct {
		name     string
		caller   CallerIdentity
		instance string
		gen      uint64
		when     time.Time
	}{
		{"other user", CallerIdentity{UserSID: "S-1-5-21-2", SessionID: 3}, testInstance, 7, now},
		{"other session", CallerIdentity{UserSID: "S-1-5-21-1", SessionID: 4}, testInstance, 7, now},
		{"other instance", caller, strings.Repeat("f", 32), 7, now},
		{"other generation", caller, testInstance, 8, now},
		{"expired", caller, testInstance, 7, expires.Add(time.Second)},
	}
	for _, c := range cases {
		if _, err := Verify(key, token, c.caller, c.instance, c.gen, c.when); err == nil {
			t.Fatalf("%s accepted", c.name)
		}
	}
	if _, err := Verify([]byte("another-key-another-key-another!"), token, caller, testInstance, 7, now); err == nil {
		t.Fatal("foreign signing key accepted")
	}
	if _, err := Verify(key, token+"x", caller, testInstance, 7, now); err == nil {
		t.Fatal("tampered capability accepted")
	}
}

func TestOwnerConflict(t *testing.T) {
	var o Owner
	now := time.Unix(1_800_000_000, 0)
	alice := CallerIdentity{UserSID: "S-1-5-21-1", SessionID: 1}
	bob := CallerIdentity{UserSID: "S-1-5-21-2", SessionID: 2}

	if _, err := o.ClaimOwner(alice, testInstance, now, time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := o.ClaimOwner(bob, testInstance, now, time.Hour); err == nil {
		t.Fatal("second user took over the broker")
	}
	if _, err := o.ClaimOwner(alice, testInstance, now.Add(30*time.Minute), time.Hour); err != nil {
		t.Fatalf("same user refresh rejected: %v", err)
	}
	// After expiry a new user may claim; this is a documented first-version
	// limitation, not a silent takeover while the first session is live.
	if _, err := o.ClaimOwner(bob, testInstance, now.Add(2*time.Hour), time.Hour); err != nil {
		t.Fatalf("expired owner blocked new claim: %v", err)
	}
	var systemOwner Owner
	if _, err := systemOwner.ClaimOwner(CallerIdentity{UserSID: "S-1-5-18", SessionID: 0, IsSystem: true}, testInstance, now, time.Hour); err == nil {
		t.Fatal("SYSTEM claimed a user capability")
	}
}
