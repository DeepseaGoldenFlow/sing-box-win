//go:build windows

package main

// Minimal named-pipe client used by the P0 security evidence
// (scripts/test-windows-service-security.ps1) and by an operator who wants to
// ask the installed broker for `ping` or the dry-run `recover` plan.
//
// It exercises the real contract: hello (impersonation + owner claim +
// capability), then ping/recover with that capability. It cannot start, stop or
// reload anything, and it never sends a profile, URL or path.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/JARVIS/sing-box-win/internal/broker"
	"github.com/JARVIS/sing-box-win/internal/platform"
	"golang.org/x/sys/windows"
)

const brokerPipe = `\\.\pipe\SFW\TUNBroker`

func openBrokerPipe() (windows.Handle, error) {
	name, err := windows.UTF16PtrFromString(brokerPipe)
	if err != nil {
		return 0, err
	}
	// The server calls ImpersonateNamedPipeClient, which uses the client's
	// default impersonation level; no override is needed here.
	return windows.CreateFile(
		name,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		0,   // no sharing: one request/response exchange
		nil, // default security
		windows.OPEN_EXISTING,
		0, // synchronous
		0,
	)
}

// roundTrip sends one JSON request and collects the single JSON response the
// server writes before disconnecting.
func roundTrip(h windows.Handle, req broker.Request) (broker.Response, error) {
	var resp broker.Response
	body, err := json.Marshal(req)
	if err != nil {
		return resp, err
	}
	var wrote uint32
	if err = windows.WriteFile(h, body, &wrote, nil); err != nil {
		return resp, err
	}
	buf := make([]byte, 64<<10)
	var raw []byte
	for {
		var n uint32
		err = windows.ReadFile(h, buf, &n, nil)
		if n > 0 {
			raw = append(raw, buf[:n]...)
		}
		if err != nil {
			if errors.Is(err, windows.ERROR_BROKEN_PIPE) || errors.Is(err, windows.ERROR_PIPE_NOT_CONNECTED) {
				break
			}
			return resp, err
		}
		if n == 0 || len(raw) >= len(buf) {
			break
		}
		// The response is complete as soon as it parses; the server closes the
		// pipe right afterwards, so do not block waiting for the close.
		if json.Unmarshal(raw, &resp) == nil {
			return resp, nil
		}
	}
	if err = json.Unmarshal(raw, &resp); err != nil {
		return resp, fmt.Errorf("broker returned an unreadable response: %w", err)
	}
	return resp, nil
}

// runClient sends hello and then the requested action, printing the broker's
// JSON answer. A broker-side rejection is returned as an error (exit 1).
func runClient(action string) error {
	instanceID, err := platform.NewInstanceID()
	if err != nil {
		return err
	}
	h, err := openBrokerPipe()
	if err != nil {
		return fmt.Errorf("connect to the broker pipe (is the SFWTunBroker service running?): %w", err)
	}
	defer windows.CloseHandle(h)

	hello, err := roundTrip(h, broker.Request{
		Version:    broker.ProtocolVersion,
		Action:     broker.ActionHello,
		InstanceID: instanceID,
	})
	if err != nil {
		return err
	}
	if !hello.OK {
		return fmt.Errorf("broker rejected hello: %s", hello.Error)
	}
	if action == "" || action == broker.ActionHello {
		return printResponse(hello)
	}
	req := broker.Request{
		Version:    broker.ProtocolVersion,
		Action:     action,
		InstanceID: instanceID,
		Capability: hello.Capability,
	}
	if action == broker.ActionRecover {
		// P0 recover is dry-run only; say so explicitly so the broker cannot
		// mistake the request for a real recovery.
		dry := true
		req.DryRun = &dry
	}
	resp, err := roundTrip(h, req)
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("broker rejected %s: %s", action, resp.Error)
	}
	return printResponse(resp)
}

func printResponse(resp broker.Response) error {
	b, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, string(b))
	return nil
}
