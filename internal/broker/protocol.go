// Package broker implements the narrow request contract shared by the TUN
// broker service and its clients. It is platform-neutral so the whitelist,
// capability signing and expiry rules are unit tested without Windows.
//
// docs/proposal-tun-windows.md §0.1 and §0.2.6: the broker accepts only fixed
// actions, never profile/URL/path/command-line parameters, issues a short-lived
// capability bound to the caller's user SID, session, engine instance and
// expiry, and binds requests to a generation so a replayed old start/stop
// cannot affect a new session.
package broker

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ProtocolVersion is the broker wire version. Unknown versions are rejected.
const ProtocolVersion = 1

// CapabilityTTL bounds how long an issued capability stays valid.
const CapabilityTTL = 2 * time.Hour

// Actions the broker understands. P0 deliberately implements only these three
// and every one of them is side-effect free: recover is dry-run only.
const (
	ActionHello   = "hello"
	ActionPing    = "ping"
	ActionVersion = "version"
	ActionRecover = "recover"
)

// Request is the fixed schema accepted from a pipe client. There is
// intentionally no profile, subscription, URL, path, executable or argument
// field: the broker must not be usable as a generic privileged launcher.
type Request struct {
	Version    int    `json:"version"`
	Action     string `json:"action"`
	InstanceID string `json:"instance_id"`
	Capability string `json:"capability,omitempty"`
	// DryRun exists only to reject a future non-dry-run recover in P0. It
	// defaults to true and cannot be used to request real changes.
	DryRun *bool `json:"dry_run,omitempty"`
}

// Response is the fixed schema returned to a pipe client. It never contains the
// broker token or capability signing key.
type Response struct {
	OK         bool        `json:"ok"`
	Error      string      `json:"error,omitempty"`
	Action     string      `json:"action,omitempty"`
	Version    int         `json:"broker_version,omitempty"`
	Protocol   int         `json:"protocol_version,omitempty"`
	Capability string      `json:"capability,omitempty"`
	Expires    string      `json:"expires,omitempty"`
	DryRun     bool        `json:"dry_run,omitempty"`
	Planned    []string    `json:"planned,omitempty"`
	Tokens     *TokenFacts `json:"tokens,omitempty"`
	// Broker is the service's own token identity, filled from its process token
	// (never from a request). It lets the P0 security script prove the broker is
	// a service identity without needing PROCESS_QUERY on a SYSTEM process.
	Broker *TokenFacts `json:"broker,omitempty"`
}

// TokenFacts is the secret-free identity of the broker and, when known, the
// caller. Security tests print it to prove UI/broker separation.
type TokenFacts struct {
	User             string `json:"user"`
	UserSID          string `json:"user_sid"`
	SessionID        uint32 `json:"session_id"`
	ElevationType    string `json:"elevation_type"`
	Elevation        bool   `json:"elevation"`
	IntegrityLevel   string `json:"integrity_level"`
	IsLocalSystem    bool   `json:"is_local_system"`
	IsAdministrators bool   `json:"is_administrators"`
}

// CallerIdentity is what the pipe server derives from SID impersonation, not
// from anything the client claims.
type CallerIdentity struct {
	UserSID   string
	SessionID uint32
	IsSystem  bool
}

// Claims bind a capability to one caller and one broker generation.
type Claims struct {
	Version    int    `json:"v"`
	UserSID    string `json:"sid"`
	SessionID  uint32 `json:"session"`
	InstanceID string `json:"instance"`
	Generation uint64 `json:"generation"`
	ExpiresAt  int64  `json:"exp"`
}

// ValidAction reports whether action is in the P0 whitelist.
func ValidAction(action string) bool {
	switch action {
	case ActionHello, ActionPing, ActionVersion, ActionRecover:
		return true
	default:
		return false
	}
}

// Validate checks the structural contract before any broker state is touched.
func (r Request) Validate() error {
	if r.Version != ProtocolVersion {
		return fmt.Errorf("unsupported broker protocol version %d", r.Version)
	}
	if !ValidAction(r.Action) {
		return errors.New("unsupported broker action")
	}
	if len(r.InstanceID) != 32 || strings.Trim(r.InstanceID, "0123456789abcdef") != "" {
		return errors.New("invalid instance_id")
	}
	if r.Action == ActionRecover {
		if r.DryRun != nil && !*r.DryRun {
			// P0 must never create adapters, routes or DNS.
			return errors.New("recover is dry-run only in this phase")
		}
	}
	return nil
}

// Sign serializes claims and appends an HMAC-SHA256 using the service key.
func Sign(key []byte, claims Claims) (string, error) {
	b, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(b)
	return base64.RawURLEncoding.EncodeToString(b) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

// Verify checks the MAC in constant time, then expiry and caller binding.
func Verify(key []byte, capability string, caller CallerIdentity, instanceID string, generation uint64, now time.Time) (Claims, error) {
	var claims Claims
	payload, signature, found := strings.Cut(capability, ".")
	if !found || payload == "" || signature == "" {
		return claims, errors.New("malformed capability")
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return claims, errors.New("malformed capability")
	}
	got, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil {
		return claims, errors.New("malformed capability")
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(raw)
	if !hmac.Equal(got, mac.Sum(nil)) {
		return claims, errors.New("invalid capability")
	}
	if err = json.Unmarshal(raw, &claims); err != nil {
		return claims, errors.New("invalid capability")
	}
	if claims.Version != ProtocolVersion {
		return claims, errors.New("capability version mismatch")
	}
	if !now.Before(time.Unix(claims.ExpiresAt, 0)) {
		return claims, errors.New("capability expired")
	}
	if claims.UserSID != caller.UserSID || claims.SessionID != caller.SessionID {
		return claims, errors.New("capability belongs to another user or session")
	}
	if claims.InstanceID != instanceID {
		return claims, errors.New("capability belongs to another engine instance")
	}
	if claims.Generation != generation {
		return claims, errors.New("capability belongs to another broker generation")
	}
	return claims, nil
}

// Owner tracks the single active TUN owner for this broker generation. P0 does
// not create a TUN, but the bookkeeping keeps the conflict rule testable so a
// second user is refused rather than silently taking over (§0.3).
type Owner struct {
	SID        string
	SessionID  uint32
	InstanceID string
	ExpiresAt  time.Time
}

// ClaimOwner returns the owner after admitting caller, or an error when another
// user/session already owns the broker.
func (o *Owner) ClaimOwner(caller CallerIdentity, instanceID string, now time.Time, ttl time.Duration) (Owner, error) {
	if caller.IsSystem {
		// The broker itself is the system side; it must not become a user's
		// capability holder.
		return Owner{}, errors.New("system callers do not need a user capability")
	}
	if o.SID != "" && now.Before(o.ExpiresAt) && (o.SID != caller.UserSID || o.SessionID != caller.SessionID) {
		return Owner{}, errors.New("another user or session owns the TUN broker")
	}
	*o = Owner{SID: caller.UserSID, SessionID: caller.SessionID, InstanceID: instanceID, ExpiresAt: now.Add(ttl)}
	return *o, nil
}

// IssueCapability signs a capability for an admitted caller. The caller identity
// must come from impersonation, never from the request body.
func IssueCapability(key []byte, caller CallerIdentity, instanceID string, generation uint64, now time.Time) (string, time.Time, error) {
	expires := now.Add(CapabilityTTL)
	token, err := Sign(key, Claims{
		Version:    ProtocolVersion,
		UserSID:    caller.UserSID,
		SessionID:  caller.SessionID,
		InstanceID: instanceID,
		Generation: generation,
		ExpiresAt:  expires.Unix(),
	})
	return token, expires, err
}

// RecoverPlan is the only answer recover can give in this phase: an explicit
// statement that no adapter/route/DNS action was taken.
func RecoverPlan() []string {
	return []string{
		"dry-run: no TUN adapter created",
		"dry-run: no route or DNS change",
		"dry-run: no host started",
	}
}
