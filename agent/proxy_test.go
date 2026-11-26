package agent

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/chainreactors/rem/protocol/core"
	"github.com/chainreactors/rem/protocol/message"
)

// The policy dialer is the containment seam for server-side agents: a refused
// dial must fail before any byte reaches the target, an allowed dial passes
// through unchanged.
func TestPolicyDialerRefusesBeforeTouchingTarget(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	accepted := make(chan net.Addr, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		accepted <- conn.RemoteAddr()
		io.Copy(io.Discard, conn) //nolint:errcheck
		conn.Close()              //nolint:errcheck
	}()

	refused := errors.New("refused by policy")
	d := &policyDialer{inner: &core.NetDialer{}, policy: func(network, addr string) error { return refused }}
	if _, err := d.DialContext(context.Background(), "tcp", ln.Addr().String()); !errors.Is(err, refused) {
		t.Fatalf("want the policy error, got %v", err)
	}
	// A dial that the policy refused must not have produced a connection on
	// the target: the fence holds before the transport is touched.
	select {
	case addr := <-accepted:
		t.Fatalf("refused dial still reached the target from %v", addr)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestPolicyDialerAllowsPassthrough(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		io.Copy(conn, conn) //nolint:errcheck // echo
		conn.Close()        //nolint:errcheck
	}()

	var dialedNetwork, dialedAddr string
	d := &policyDialer{inner: &core.NetDialer{}, policy: func(network, addr string) error {
		dialedNetwork, dialedAddr = network, addr
		return nil
	}}
	conn, err := d.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("allowed dial failed: %v", err)
	}
	defer conn.Close()
	if dialedNetwork != "tcp" || dialedAddr != ln.Addr().String() {
		t.Fatalf("policy saw network=%q addr=%q, want tcp %s", dialedNetwork, dialedAddr, ln.Addr())
	}
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(buf) != "ping" {
		t.Fatalf("echo broken through the policy dialer: %q", buf)
	}
}

// A forked child builds its Config through Clone and shares the parent's
// client dialer; the policy must survive the Clone, or a relay's children
// would dial outside the embedder's fence.
func TestConfigCloneCarriesDialPolicy(t *testing.T) {
	policy := func(network, addr string) error { return errors.New("fenced") }
	consoleURL, err := core.NewURL("tcp://127.0.0.1:1")
	if err != nil {
		t.Fatalf("NewURL: %v", err)
	}
	cfg := &Config{
		Alias:      "parent",
		DialPolicy: policy,
		URLs:       &core.URLs{ConsoleURL: consoleURL},
	}
	child := cfg.Clone(&message.Control{Source: "child", Local: "raw://127.0.0.1:2", Remote: "raw://127.0.0.1:3"})
	if child.DialPolicy == nil {
		t.Fatal("Clone dropped the dial policy: forked children would escape the fence")
	}
	if err := child.DialPolicy("tcp", "127.0.0.1:9"); err == nil {
		t.Fatal("cloned policy is not the same function")
	}
}
