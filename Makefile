GO ?= go
export CGO_ENABLED=0
export GOFLAGS=-mod=mod -tags=with_gvisor,with_quic,with_utls,with_clash_api
export GOTOOLCHAIN=go1.26.10
.PHONY: build check
build:
	GOOS=windows GOARCH=amd64 $(GO) build -o build/singbox-win.exe ./cmd/singbox-win
check:
	$(GO) vet ./...
	GOOS=windows GOARCH=amd64 $(GO) vet ./...
	$(GO) test ./internal/config
