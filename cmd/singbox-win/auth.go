package main

// Engine API authentication (docs/proposal-tun-windows.md §0.2.4 and §0.2.5).
//
// The engine listens on loopback for the native UI and the single-instance
// forwarder. Loopback is not an authorization boundary: any local process can
// connect to 127.0.0.1. Every /api/* request therefore has to present the
// per-start bearer token from the ACL-protected run directory before the router
// sees it. Host/Origin checks stay as defense in depth but are not authentication.

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

const instanceHeader = "X-SFW-Instance"

// engineAuth holds the current process token. It is never serialized, logged or
// written outside the protected token file.
type engineAuth struct {
	token      string
	instanceID string
}

func newEngineAuth(token, instanceID string) *engineAuth {
	if token == "" || instanceID == "" {
		// Fail closed: a misconfigured engine must not serve an unauthenticated API.
		return nil
	}
	return &engineAuth{token: token, instanceID: instanceID}
}

// tokenFromAuthorization accepts exactly one "Bearer <token>" scheme. A missing
// header, an empty token, another scheme, or extra whitespace/schemes all fail.
func tokenFromAuthorization(header string) (string, bool) {
	scheme, rest, found := strings.Cut(header, " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	if rest == "" || strings.ContainsAny(rest, " \t") {
		return "", false
	}
	return rest, true
}

// authorized reports whether the request carries this engine's token. The
// comparison is constant time for equal lengths; length mismatch is rejected
// without comparison because the token length is fixed and public.
func (a *engineAuth) authorized(r *http.Request) bool {
	if a == nil {
		return false
	}
	presented, ok := tokenFromAuthorization(r.Header.Get("Authorization"))
	if !ok {
		return false
	}
	if len(presented) != len(a.token) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(presented), []byte(a.token)) == 1
}

// activationInstanceMatches binds /api/activate to the engine instance named in
// runtime.json. A stale runtime record (or a copied token) cannot direct a deep
// link or file path into a newer engine.
func (a *engineAuth) activationInstanceMatches(r *http.Request) bool {
	if a == nil || a.instanceID == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(r.Header.Get(instanceHeader)), []byte(a.instanceID)) == 1
}

// isAPIPath is the gate's path predicate. It intentionally matches the exact
// "/api" as well, so a request with a slightly different spelling never reaches
// the router unauthenticated; ServeMux may redirect unclean paths before the
// handler body runs, but the redirect itself reveals nothing.
func isAPIPath(path string) bool {
	return path == "/api" || strings.HasPrefix(path, "/api/")
}

// httpHandler applies host/origin checks, then the bearer gate for every /api/
// path, and only then calls the router.
func (a *App) httpHandler(mux *http.ServeMux, host, base string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != host {
			http.Error(w, "invalid host", http.StatusForbidden)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && origin != base {
			http.Error(w, "invalid origin", http.StatusForbidden)
			return
		}
		if isAPIPath(r.URL.Path) {
			if !a.auth.authorized(r) {
				w.Header().Set("WWW-Authenticate", `Bearer realm="sing-box-win"`)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self'; frame-ancestors 'none'")
		mux.ServeHTTP(w, r)
	})
}
