package gcp

import (
	"context"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"sync"

	jwt "github.com/dgrijalva/jwt-go"
	"github.com/hashicorp/hcl"

	"github.com/spiffe/spire/pkg/common/plugin/gcp"
	"github.com/spiffe/spire/proto/agent/nodeattestor"
	"github.com/spiffe/spire/proto/common"
	spi "github.com/spiffe/spire/proto/common/plugin"
)

const (
	identityTokenURLHost         = "metadata.google.internal"
	identityTokenURLPathTemplate = "/computeMetadata/v1/instance/service-accounts/%s/identity"
	identityTokenAudience        = "spire-gcp-node-attestor"

	defaultServiceAccount = "default"
)

type IITAttestorConfig struct {
	trustDomain    string
	ServiceAccount string `hcl:"service_account"`
}

type IITAttestorPlugin struct {
	tokenHost string

	mtx    sync.RWMutex
	config *IITAttestorConfig
}

func IdentityTokenURL(host, serviceAccount string) string {
	query := url.Values{}
	query.Set("audience", identityTokenAudience)
	query.Set("format", "full")
	url := &url.URL{
		Scheme:   "http",
		Host:     host,
		Path:     fmt.Sprintf(identityTokenURLPathTemplate, serviceAccount),
		RawQuery: query.Encode(),
	}
	return url.String()
}

func retrieveInstanceIdentityToken(url string) ([]byte, error) {
	client := &http.Client{}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Metadata-Flavor", "Google")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	bytes, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return bytes, nil
}

func RetrieveValidInstanceIdentityToken(url string) (*gcp.IdentityToken, []byte, error) {
	identityTokenBytes, err := retrieveInstanceIdentityToken(url)
	if err != nil {
		return nil, nil, err
	}

	identityToken := &gcp.IdentityToken{}
	if _, _, err := new(jwt.Parser).ParseUnverified(string(identityTokenBytes), identityToken); err != nil {
		return nil, nil, newErrorf("unable to parse identity token: %v", err)
	}

	if identityToken.Google == (gcp.Google{}) {
		return nil, nil, newError("identity token is missing google claims")
	}

	return identityToken, identityTokenBytes, nil
}

func (p *IITAttestorPlugin) FetchAttestationData(stream nodeattestor.FetchAttestationData_PluginStream) error {
	c, err := p.getConfig()
	if err != nil {
		return err
	}

	identityToken, identityTokenBytes, err := RetrieveValidInstanceIdentityToken(identityTokenURL(p.tokenHost, c.ServiceAccount))
	if err != nil {
		return newErrorf("unable to retrieve valid identity token: %v", err)
	}

	spiffeID := gcp.MakeSpiffeID(p.config.trustDomain, identityToken.Google.ComputeEngine.ProjectID, identityToken.Google.ComputeEngine.InstanceID)
	resp, err := BuildAttestationResponse(spiffeID, gcp.PluginName, identityTokenBytes)
	if err != nil {
		return err
	}

	if err := stream.Send(resp); err != nil {
		return err
	}

	return nil
}

func BuildAttestationResponse(spiffeID string, pluginName string, identityTokenBytes []byte) (*nodeattestor.FetchAttestationDataResponse, error) {

	data := &common.AttestationData{
		Type: pluginName,
		Data: identityTokenBytes,
	}

	resp := &nodeattestor.FetchAttestationDataResponse{
		AttestationData: data,
		SpiffeId:        spiffeID,
	}
	return resp, nil
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

	if config.ServiceAccount == "" {
		config.ServiceAccount = defaultServiceAccount
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
		tokenHost: identityTokenURLHost,
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
