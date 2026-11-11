GO ?= go
export CGO_ENABLED=0
export GOFLAGS=-mod=mod -tags=with_gvisor,with_quic,with_utls,with_clash_api
export GOTOOLCHAIN=go1.26.10
.PHONY: build check
build:
	GOOS=windows GOARCH=amd64 $(GO) build -ldflags '-H windowsgui -s -w' -o build/sing-box-win.exe ./cmd/singbox-win
# The broker is a console-subsystem service host, so it must NOT use -H windowsgui.
	GOOS=windows GOARCH=amd64 $(GO) build -ldflags '-s -w' -o build/sfw-tun-broker.exe ./cmd/sfw-tun-broker
check:
	$(GO) vet ./...
	GOOS=windows GOARCH=amd64 $(GO) vet ./...
	$(GO) test ./internal/... ./cmd/singbox-win
