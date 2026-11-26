package agent

import (
	"context"
	"encoding/hex"
	"net"

	"github.com/chainreactors/proxyclient"
	"github.com/chainreactors/rem/protocol/core"
	"github.com/chainreactors/rem/x/cryptor"
)

func (agent *Agent) initProxyClient() error {
	var client core.ContextDialer
	if len(agent.Proxies) == 0 {
		client = &core.NetDialer{}
	} else {
		chain, err := proxyclient.NewClientChain(agent.Proxies)
		if err != nil {
			return err
		}
		client = core.NewProxyDialer(chain)
	}
	// The dial policy wraps the real dialer so every consumer of agent.client
	// (outbound serves, bridges, forks through the shared parent dialer) is
	// fenced by the same check.
	if agent.DialPolicy != nil {
		client = &policyDialer{inner: client, policy: agent.DialPolicy}
	}
	agent.client = client
	return nil
}

// policyDialer consults the agent's dial policy before the dial reaches the
// wrapped dialer: a refused dial never touches the network. It exists so an
// embedder accepting untrusted server-side clients can bound what those
// clients make the server dial (runner.Policy.OnDial, docs/15 §9.1).
type policyDialer struct {
	inner  core.ContextDialer
	policy func(network, addr string) error
}

func (d *policyDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if d.policy != nil {
		if err := d.policy(network, address); err != nil {
			return nil, err
		}
	}
	return d.inner.DialContext(ctx, network, address)
}

func (d *policyDialer) Dial(network, address string) (net.Conn, error) {
	return d.DialContext(context.Background(), network, address)
}

func buildAuthToken(key []byte) (string, error) {
	token, err := cryptor.AesEncrypt(key, cryptor.PKCS7Padding(key, 16))
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(token), nil
}
