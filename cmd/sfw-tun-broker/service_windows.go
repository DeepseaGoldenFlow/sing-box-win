//go:build windows

package main

import (
	"fmt"

	"github.com/JARVIS/sing-box-win/internal/broker"
	"golang.org/x/sys/windows/svc"
)

const serviceName = "SFWTunBroker"

// brokerHandler is the service main loop. It starts the pipe server and stops
// it cleanly; there is no TUN host in this phase.
type brokerHandler struct {
	server *broker.Server
	stop   chan struct{}
}

func (h *brokerHandler) Execute(args []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	const accepted = svc.AcceptStop | svc.AcceptShutdown
	changes <- svc.Status{State: svc.StartPending}
	if h.server == nil {
		server, err := broker.NewServer()
		if err != nil {
			return false, 1
		}
		h.server = server
	}
	h.stop = make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- h.server.Serve(h.stop) }()
	changes <- svc.Status{State: svc.Running, Accepts: accepted}
	for {
		select {
		case err := <-done:
			if err != nil {
				return false, 2
			}
			changes <- svc.Status{State: svc.Stopped}
			return false, 0
		case c := <-r:
			switch c.Cmd {
			case svc.Interrogate:
				changes <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				changes <- svc.Status{State: svc.StopPending}
				close(h.stop)
				return false, 0
			}
		}
	}
}

func runService() error {
	isService, err := svc.IsWindowsService()
	if err != nil {
		return err
	}
	if !isService {
		return fmt.Errorf("%s must be started by the service control manager", serviceName)
	}
	return svc.Run(serviceName, &brokerHandler{})
}
