//go:build windows

package broker

// Named-pipe server for the TUN broker (docs/proposal-tun-windows.md §0.1 and
// §0.2.6). P0 starts this only via an explicitly approved service installation;
// it never creates an adapter, route or DNS change, and it never forwards
// profile/subscription/path parameters anywhere.

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"github.com/JARVIS/sing-box-win/internal/platform"
	"golang.org/x/sys/windows"
)

const (
	maxRequestBytes = 4096
	maxInstances    = 8
	// pipeName is fixed by the service; clients may enumerate it but every
	// request still needs an impersonation check plus a capability.
	pipeName = `\\.\pipe\SFW\TUNBroker`
)

// Server is one broker generation. Restarting the service mints a new key and
// generation, so capabilities from the previous process stop verifying.
type Server struct {
	generation uint64
	key        []byte
	ownerMu    sync.Mutex
	owner      Owner
	now        func() time.Time
}

// NewServer mints the service token used to sign capabilities. It is separate
// from the user engine token (§0.2.6) and never exposed over the pipe.
func NewServer() (*Server, error) {
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		return nil, err
	}
	var g [8]byte
	if _, err := rand.Read(g[:]); err != nil {
		return nil, err
	}
	var generation uint64
	for _, b := range g {
		generation = generation<<8 | uint64(b)
	}
	if generation == 0 {
		generation = 1
	}
	return &Server{generation: generation, key: key[:], now: time.Now}, nil
}

// pipeSDDL grants the interactive user and SYSTEM access to connect. It is the
// first of two checks: the second is SID impersonation of the actual client.
const pipeSDDL = "D:(A;;GA;;;SY)(A;;GA;;;IU)"

func (s *Server) generationValue() uint64 { return s.generation }

// CallerForPipe derives the client identity from its impersonation token. A
// claim in the request body is never trusted for this.
//
// Impersonation is per OS thread, and the Go scheduler may move a goroutine to
// a different thread at any preemption point. The goroutine is therefore pinned
// for the whole impersonate/read-token/revert sequence; otherwise we could read
// this process's own token and mistake SYSTEM for the caller.
func CallerForPipe(pipe windows.Handle) (CallerIdentity, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := impersonateNamedPipeClient(pipe); err != nil {
		return CallerIdentity{}, err
	}
	// Revert on the same locked thread. The deferred call runs before
	// UnlockOSThread (defers execute LIFO), so this is the thread still holding
	// the impersonation.
	defer func() { _ = windows.RevertToSelf() }()
	// GetCurrentThreadToken returns a pseudo handle, which must not be closed
	// (CloseHandle would fail with ERROR_INVALID_HANDLE).
	token := windows.GetCurrentThreadToken()
	if token == 0 {
		return CallerIdentity{}, errors.New("impersonation token unavailable")
	}
	user, err := token.GetTokenUser()
	if err != nil {
		return CallerIdentity{}, err
	}
	var session uint32
	var outLen uint32
	if err = windows.GetTokenInformation(token, windows.TokenSessionId, (*byte)(unsafe.Pointer(&session)), uint32(unsafe.Sizeof(session)), &outLen); err != nil {
		return CallerIdentity{}, err
	}
	systemSID, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return CallerIdentity{}, err
	}
	return CallerIdentity{
		UserSID:   user.User.Sid.String(),
		SessionID: session,
		IsSystem:  user.User.Sid.Equals(systemSID),
	}, nil
}

// Handle executes one validated request. It is split from the pipe loop so the
// whitelist and capability rules are testable without a service.
func (s *Server) Handle(caller CallerIdentity, raw []byte) Response {
	var req Request
	if err := json.Unmarshal(raw, &req); err != nil {
		return Response{Error: "malformed request"}
	}
	if err := req.Validate(); err != nil {
		return Response{Error: err.Error()}
	}
	now := s.now()
	switch req.Action {
	case ActionHello:
		if caller.IsSystem {
			return Response{Error: "system callers do not need a capability"}
		}
		s.ownerMu.Lock()
		_, err := s.owner.ClaimOwner(caller, req.InstanceID, now, CapabilityTTL)
		s.ownerMu.Unlock()
		if err != nil {
			return Response{Error: err.Error()}
		}
		capability, expires, err := IssueCapability(s.key, caller, req.InstanceID, s.generation, now)
		if err != nil {
			return Response{Error: "capability issuance failed"}
		}
		return Response{
			OK:         true,
			Action:     ActionHello,
			Capability: capability,
			Expires:    expires.UTC().Format(time.RFC3339),
			Protocol:   ProtocolVersion,
			Tokens:     callerFacts(caller, now),
			Broker:     selfFacts(),
		}
	case ActionPing:
		if _, err := Verify(s.key, req.Capability, caller, req.InstanceID, s.generation, now); err != nil {
			return Response{Action: ActionPing, Error: err.Error()}
		}
		return Response{OK: true, Action: ActionPing, Protocol: ProtocolVersion, Tokens: callerFacts(caller, now), Broker: selfFacts()}
	case ActionVersion:
		return Response{OK: true, Action: ActionVersion, Version: 1, Protocol: ProtocolVersion, Broker: selfFacts()}
	case ActionRecover:
		// Recover is dry-run in P0 and requires a capability: it must not be a
		// way to obtain privileged actions without user consent.
		if _, err := Verify(s.key, req.Capability, caller, req.InstanceID, s.generation, now); err != nil {
			return Response{Action: ActionRecover, Error: err.Error()}
		}
		return Response{OK: true, Action: ActionRecover, DryRun: true, Planned: RecoverPlan(), Protocol: ProtocolVersion, Broker: selfFacts()}
	}
	return Response{Error: "unsupported broker action"}
}

// callerFacts is derived from the impersonated token, not the request.
func callerFacts(caller CallerIdentity, now time.Time) *TokenFacts {
	_ = now
	return &TokenFacts{UserSID: caller.UserSID, SessionID: caller.SessionID, IsLocalSystem: caller.IsSystem}
}

// selfFacts reports the broker's own token identity. The service process knows
// it without PROCESS_QUERY rights, which a normal user does not have on a
// SYSTEM process. It is secret-free (see platform.TokenInfo).
func selfFacts() *TokenFacts {
	info, err := platform.CurrentTokenInfo()
	if err != nil {
		return nil
	}
	return &TokenFacts{
		User:             info.User,
		UserSID:          info.UserSID,
		SessionID:        info.SessionID,
		ElevationType:    info.ElevationType,
		Elevation:        info.Elevation,
		IntegrityLevel:   info.IntegrityLevel,
		IsLocalSystem:    info.IsLocalSystem,
		IsAdministrators: info.IsAdministrators,
	}
}

// Serve listens on the fixed pipe until stop is closed. Each connection serves
// exactly one request, like a small RPC endpoint.
func (s *Server) Serve(stop <-chan struct{}) error {
	sd, err := windows.SecurityDescriptorFromString(pipeSDDL)
	if err != nil {
		return err
	}
	sa := &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	name, err := windows.UTF16PtrFromString(pipeName)
	if err != nil {
		return err
	}
	for {
		select {
		case <-stop:
			return nil
		default:
		}
		h, err := windows.CreateNamedPipe(
			name,
			// Overlapped I/O is what lets the service stop cleanly: a synchronous
			// ConnectNamedPipe blocks until some client connects, which would
			// ignore svc.Stop for an indefinite time.
			windows.PIPE_ACCESS_DUPLEX|windows.FILE_FLAG_OVERLAPPED|windows.FILE_FLAG_FIRST_PIPE_INSTANCE,
			windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT|windows.PIPE_REJECT_REMOTE_CLIENTS,
			maxInstances, maxRequestBytes, maxRequestBytes, 0, sa,
		)
		if err != nil {
			// Another broker instance owns the name; a second high-privilege
			// broker must not run.
			if errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, windows.ERROR_PIPE_BUSY) {
				return fmt.Errorf("broker pipe already exists: %w", err)
			}
			return err
		}
		connected, err := waitForClient(h, stop)
		if err != nil {
			windows.CloseHandle(h)
			return err
		}
		if !connected {
			windows.CloseHandle(h)
			return nil
		}
		s.serveConn(h)
		windows.DisconnectNamedPipe(h)
		windows.CloseHandle(h)
	}
}

// waitForClient connects the pipe with an overlapped ConnectNamedPipe. It waits
// on the stop channel as well, so a service stop is not delayed by a client that
// never arrives. A client that connects between CreateNamedPipe and
// ConnectNamedPipe is reported as ERROR_PIPE_CONNECTED and is already connected.
func waitForClient(h windows.Handle, stop <-chan struct{}) (bool, error) {
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return false, err
	}
	defer windows.CloseHandle(event)
	overlapped := &windows.Overlapped{HEvent: event}
	err = windows.ConnectNamedPipe(h, overlapped)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, windows.ERROR_PIPE_CONNECTED) {
		return true, nil
	}
	if !errors.Is(err, windows.ERROR_IO_PENDING) {
		return false, err
	}
	for {
		result, err := windows.WaitForSingleObject(event, 200)
		if err != nil {
			return false, err
		}
		switch result {
		case windows.WAIT_OBJECT_0:
			var n uint32
			if err = windows.GetOverlappedResult(h, overlapped, &n, false); err != nil {
				if errors.Is(err, windows.ERROR_PIPE_CONNECTED) {
					return true, nil
				}
				if errors.Is(err, windows.ERROR_OPERATION_ABORTED) {
					return false, nil
				}
				return false, err
			}
			return true, nil
		case uint32(windows.WAIT_TIMEOUT):
			select {
			case <-stop:
				// Cancel the pending connect and drain it before the handle is
				// closed, so the overlapped structure is not in use afterwards.
				_ = windows.CancelIoEx(h, overlapped)
				var n uint32
				_ = windows.GetOverlappedResult(h, overlapped, &n, true)
				return false, nil
			default:
			}
		default:
			return false, fmt.Errorf("ConnectNamedPipe wait failed: %d", result)
		}
	}
}

func (s *Server) serveConn(h windows.Handle) {
	caller, err := CallerForPipe(h)
	if err != nil {
		writeResponse(h, Response{Error: "caller verification failed"})
		return
	}
	request, err := readRequest(h)
	if err != nil {
		writeResponse(h, Response{Error: "read failed"})
		return
	}
	writeResponse(h, s.Handle(caller, request))
}

// requestTimeout bounds how long a connected client may take to send its one
// request. A client that stalls must not pin the service forever.
const requestTimeout = 10 * time.Second

// waitIO waits for an overlapped operation to finish and returns the byte count.
// On timeout it cancels the operation and drains it before returning, so the
// OVERLAPPED is not still referenced when the caller closes the handle.
func waitIO(h windows.Handle, overlapped *windows.Overlapped, timeout time.Duration) (uint32, error) {
	deadline := time.Now().Add(timeout)
	for {
		result, err := windows.WaitForSingleObject(overlapped.HEvent, 200)
		if err != nil {
			return 0, err
		}
		switch result {
		case windows.WAIT_OBJECT_0:
			var n uint32
			if err = windows.GetOverlappedResult(h, overlapped, &n, false); err != nil {
				return 0, err
			}
			return n, nil
		case uint32(windows.WAIT_TIMEOUT):
			if time.Now().Before(deadline) {
				continue
			}
			_ = windows.CancelIoEx(h, overlapped)
			var n uint32
			_ = windows.GetOverlappedResult(h, overlapped, &n, true)
			return 0, errors.New("pipe I/O timed out")
		default:
			return 0, fmt.Errorf("pipe wait failed: %d", result)
		}
	}
}

// readRequest reads one request from an overlapped pipe handle. The handle is
// created with FILE_FLAG_OVERLAPPED, so ReadFile must be given an OVERLAPPED;
// passing nil would fail with ERROR_INVALID_PARAMETER.
func readRequest(h windows.Handle) ([]byte, error) {
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(event)
	buf := make([]byte, maxRequestBytes)
	overlapped := &windows.Overlapped{HEvent: event}
	var n uint32
	err = windows.ReadFile(h, buf, &n, overlapped)
	if err != nil {
		if !errors.Is(err, windows.ERROR_IO_PENDING) {
			return nil, err
		}
		if n, err = waitIO(h, overlapped, requestTimeout); err != nil {
			return nil, err
		}
	}
	return buf[:n], nil
}

func writeResponse(h windows.Handle, resp Response) {
	b, err := json.Marshal(resp)
	if err != nil {
		b = []byte(`{"ok":false,"error":"response encoding failed"}`)
	}
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return
	}
	defer windows.CloseHandle(event)
	overlapped := &windows.Overlapped{HEvent: event}
	var wrote uint32
	err = windows.WriteFile(h, b, &wrote, overlapped)
	if err != nil && errors.Is(err, windows.ERROR_IO_PENDING) {
		_, _ = waitIO(h, overlapped, requestTimeout)
	}
}

// impersonateNamedPipeClient is not wrapped by golang.org/x/sys/windows, so it
// is called directly. It switches the calling thread to the client's security
// context; the caller must call RevertToSelf before serving anything else.
func impersonateNamedPipeClient(pipe windows.Handle) error {
	proc := windows.NewLazySystemDLL("advapi32.dll").NewProc("ImpersonateNamedPipeClient")
	r, _, err := proc.Call(uintptr(pipe))
	if r == 0 {
		return err
	}
	return nil
}
