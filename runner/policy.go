package runner

import (
	"github.com/chainreactors/rem/protocol/core"
	"github.com/chainreactors/rem/protocol/message"
)

// Policy is the server-side ingress seam for embedders (docs/15-remote-nodes.md §9.1).
//
// The server normally trusts the client completely: the client dictates which
// side listens and which address the server dials, and every login is only
// guarded by the shared token. An embedder that accepts untrusted clients on
// a public ingress needs to reject logins, refuse the serve topology a client
// dictates, and fence the outbound dials performed on those clients' behalf.
// Each hook is optional; a nil hook — or a nil Policy — keeps the original
// behaviour exactly. The hooks never touch the wire format.
type Policy struct {
	// OnLogin runs after the login token has been verified, before the success
	// Ack is written, so a rejected login never sees a success handshake. It
	// also covers half-channel and ConnHub logins, which all enter through
	// Console.Accept. Returning an error answers Ack failed and closes the
	// connection; the server keeps accepting.
	OnLogin func(login *message.Login) error

	// OnControl runs after the serve Control has been read, before the
	// server-side agent is created. Local and Remote are the serve pair the
	// client demands and InboundSide picks which side listens and which side
	// dials — on the server they decide what this process binds and dials.
	// Returning an error answers Ack failed and closes the connection.
	OnControl func(login *message.Login, local, remote *core.URL, inboundSide string) error

	// OnDial gates every outbound dial the accepted agent performs on behalf
	// of its client (serve outbound legs, bridges). It is carried into
	// agent.Config.DialPolicy, so forked children inherit it through the
	// parent's wrapped dialer. Returning an error refuses that dial.
	OnDial func(network, addr string) error
}
