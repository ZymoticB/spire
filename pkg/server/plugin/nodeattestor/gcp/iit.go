package gcp

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/hashicorp/hcl"

	jwt "github.com/dgrijalva/jwt-go"
	"github.com/spiffe/spire/pkg/common/plugin/gcp"
	spi "github.com/spiffe/spire/proto/common/plugin"
	"github.com/spiffe/spire/proto/server/nodeattestor"
)

const (
	tokenAudience = "spire-gcp-node-attestor"
	googleCertURL = "https://www.googleapis.com/oauth2/v1/certs"
)

type tokenKeyRetriever interface {
	retrieveKey(token *jwt.Token) (interface{}, error)
}

type IITAttestorConfig struct {
	trustDomain        string
	ProjectIDWhitelist []string `hcl:"projectid_whitelist"`
}

type BaseIITAttestorPlugin struct {
	tokenKeyRetriever tokenKeyRetriever
}

type IITAttestorPlugin struct {
	BaseIITAttestorPlugin

	config *IITAttestorConfig
	mtx    sync.Mutex
}

func (p *BaseIITAttestorPlugin) ValidateIdentityAndExtractMetadata(stream nodeattestor.Attest_PluginStream, pluginName string) (gcp.ComputeEngine, error) {
	req, err := stream.Recv()
	if err != nil {
		return gcp.ComputeEngine{}, err
	}

	attestationData := req.GetAttestationData()
	if attestationData == nil {
		return gcp.ComputeEngine{}, newError("request missing attestation data")
	}

	if attestationData.Type != pluginName {
		return gcp.ComputeEngine{}, newErrorf("unexpected attestation data type %q", pluginName)
	}

	if req.AttestedBefore {
		return gcp.ComputeEngine{}, newError("instance ID has already been attested")
	}

	identityToken := &gcp.IdentityToken{}
	_, err = jwt.ParseWithClaims(string(req.GetAttestationData().Data), identityToken, p.tokenKeyRetriever.retrieveKey)
	if err != nil {
		return gcp.ComputeEngine{}, newErrorf("unable to parse/validate the identity token: %v", err)
	}

	if identityToken.Audience != tokenAudience {
		return gcp.ComputeEngine{}, newErrorf("unexpected identity token audience %q", identityToken.Audience)
	}

	return identityToken.Google.ComputeEngine, nil
}

func (p *IITAttestorPlugin) Attest(stream nodeattestor.Attest_PluginStream) error {
	c, err := p.getConfig()
	if err != nil {
		return err
	}

	identityMetadata, err := p.ValidateIdentityAndExtractMetadata(stream, gcp.PluginName)
	if err != nil {
		return err
	}

	projectIDMatchesWhitelist := false
	for _, projectID := range c.ProjectIDWhitelist {
		if identityMetadata.ProjectID == projectID {
			projectIDMatchesWhitelist = true
			break
		}
	}
	if !projectIDMatchesWhitelist {
		return newErrorf("identity token project ID %q is not in the whitelist", identityMetadata.ProjectID)
	}

	spiffeID := gcp.MakeSpiffeID(c.trustDomain, identityMetadata.ProjectID, identityMetadata.InstanceID)

	resp := &nodeattestor.AttestResponse{
		Valid:        true,
		BaseSPIFFEID: spiffeID,
	}

	if err := stream.Send(resp); err != nil {
		return err
	}

	return nil
}

func (p *IITAttestorPlugin) Configure(ctx context.Context, req *spi.ConfigureRequest) (*spi.ConfigureResponse, error) {
	config := &IITAttestorConfig{}
	if err := hcl.Decode(config, req.Configuration); err != nil {
		return nil, newErrorf("unable to decode configuration: %v", err)
	}

	if req.GlobalConfig == nil {
		return nil, newError("global configuration is required")
	}

	if req.GlobalConfig.TrustDomain == "" {
		return nil, newError("trust_domain is required")
	}
	config.trustDomain = req.GlobalConfig.TrustDomain

	if len(config.ProjectIDWhitelist) == 0 {
		return nil, newError("projectid_whitelist is required")
	}

	p.mtx.Lock()
	defer p.mtx.Unlock()

	p.config = config

	return &spi.ConfigureResponse{}, nil
}

func (*IITAttestorPlugin) GetPluginInfo(ctx context.Context, req *spi.GetPluginInfoRequest) (*spi.GetPluginInfoResponse, error) {
	return &spi.GetPluginInfoResponse{}, nil
}

func NewIITAttestorPlugin() *IITAttestorPlugin {
	return &IITAttestorPlugin{
		BaseIITAttestorPlugin: NewBaseIITAttestorPlugin(),
	}
}

func NewBaseIITAttestorPlugin() BaseIITAttestorPlugin {
	return BaseIITAttestorPlugin{
		tokenKeyRetriever: newGooglePublicKeyRetriever(googleCertURL),
	}
}

func (p *IITAttestorPlugin) getConfig() (*IITAttestorConfig, error) {
	p.mtx.Lock()
	defer p.mtx.Unlock()

	if p.config == nil {
		return nil, newError("not configured")
	}
	return p.config, nil
}

func newError(msg string) error {
	return errors.New("gcp-iit: " + msg)
}

func newErrorf(format string, args ...interface{}) error {
	return fmt.Errorf("gcp-iit: "+format, args...)
}
