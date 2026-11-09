// Package config uses sing-box's context-aware decoder without intermediate JSON models.
package config

import (
	"context"
	box "github.com/sagernet/sing-box"
	_ "github.com/sagernet/sing-box/experimental/clashapi"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/protocol/tailscale"
)

func Context() context.Context {
	ep := include.EndpointRegistry()
	tailscale.RegisterEndpoint(ep)
	dns := include.DNSTransportRegistry()
	tailscale.RegistryTransport(dns)
	return box.Context(context.Background(), include.InboundRegistry(), include.OutboundRegistry(), ep, dns, include.ServiceRegistry(), include.CertificateProviderRegistry())
}
func Parse(data []byte) (option.Options, error) {
	var opt option.Options
	err := opt.UnmarshalJSONContext(Context(), data)
	return opt, err
}
