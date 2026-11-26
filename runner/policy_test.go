//go:build !tinygo

package runner

import (
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/chainreactors/rem/protocol/core"
	"github.com/chainreactors/rem/protocol/message"
)

// End-to-end tests for the server ingress policy (runner/policy.go,
// docs/15-remote-nodes.md §9.1): a public ingress must reject logins, refuse
// the serve topology a client dictates, and fence the dials the server makes
// on the client's behalf. A nil policy must behave exactly as before.
//
// The rejection tests keep the client in-process (its handshake fails before
// any registration). The accept tests run the client through the same
// subprocess helper the e2e matrix uses: client and server agents register in
// the same package-global registry under the protocol's single login alias,
// so two live consoles in one process fight over that slot and the second
// registration always loses.

const policyTestKey = "policy-test-key"

func policyFreePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	defer ln.Close() //nolint:errcheck
	return ln.Addr().(*net.TCPAddr).Port
}

func startPolicyServer(t *testing.T, policy *Policy) (link string, con *Console) {
	t.Helper()
	var opt Options
	cmdline := fmt.Sprintf("-s tcp://127.0.0.1:%d -k %s -i 127.0.0.1 --no-sub -q", policyFreePort(t), policyTestKey)
	if err := opt.ParseArgs(strings.Fields(cmdline)); err != nil {
		t.Fatalf("server ParseArgs: %v", err)
	}
	r, err := opt.Prepare()
	if err != nil {
		t.Fatalf("server Prepare: %v", err)
	}
	r.Policy = policy
	r.URLs.ConsoleURL = r.ConsoleURLs[0]
	con, err = NewConsole(r, r.URLs)
	if err != nil {
		t.Fatalf("NewConsole: %v", err)
	}
	// The client must dial the server's own link: it carries the wrapper
	// negotiation the tunnel handshake needs; a bare tcp:// URL resets.
	// ConsoleURL is the same URL Link() publishes (the wrapper query is set
	// in NewConsole), read before Run because Link needs a live listener.
	link = con.ConsoleURL.String()
	go func() { _ = con.Run() }() //nolint:errcheck
	t.Cleanup(func() { _ = con.Close() })
	return link, con
}

// startPolicyClient runs the client console in a subprocess (same helper the
// e2e matrix uses), so its agent registry never collides with the server's.
func startPolicyClient(t *testing.T, link, alias, serves string) {
	t.Helper()
	startHelper(t, fmt.Sprintf("-c %s -k %s -q -a %s %s", link, policyTestKey, alias, serves))
}

// dialClientUntil retries past the server's listener startup window and
// returns the outcome of the final dial attempt.
func dialClientUntil(t *testing.T, con *Console, deadline time.Duration) error {
	t.Helper()
	var lastErr error
	for start := time.Now(); time.Since(start) < deadline; time.Sleep(150 * time.Millisecond) {
		_, err := con.Dial(con.ConsoleURL)
		if err == nil {
			return nil
		}
		lastErr = err
		if strings.Contains(err.Error(), "connection refused") {
			continue // listener not up yet
		}
		return err
	}
	return lastErr
}

// waitReadyServerAgent waits until the server console owns a ready agent with
// the alias, or fails after a deadline.
func waitReadyServerAgent(t *testing.T, server *Console, alias string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if a, ok := server.Agent(alias); ok && a.Init && !a.IsClosed() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("agent %s never became ready on the server", alias)
}

// assertAbsentServerAgent gives a rejected handshake a settle window, then
// requires that the server never registered the alias.
func assertAbsentServerAgent(t *testing.T, server *Console, alias string) {
	t.Helper()
	time.Sleep(2 * time.Second)
	if a, ok := server.Agent(alias); ok && a != nil && a.Init {
		t.Fatalf("agent %s registered despite the policy", alias)
	}
}

func TestPolicyNilAcceptsLoginAndControl(t *testing.T) {
	link, server := startPolicyServer(t, nil)

	startPolicyClient(t, link, "policy-nil", "-l socks5://127.0.0.1:0")
	waitReadyServerAgent(t, server, "policy-nil")
}

func TestPolicyLoginHookRejects(t *testing.T) {
	var gotAlias string
	link, server := startPolicyServer(t, &Policy{
		OnLogin: func(login *message.Login) error {
			gotAlias = login.Agent
			return fmt.Errorf("alias %s is not expected", login.Agent)
		},
	})

	client := newPolicyClient(t, link, "policy-login", "-l socks5://127.0.0.1:0")
	err := dialClientUntil(t, client, 15*time.Second)
	if err == nil {
		t.Fatal("login accepted although the policy refuses every login")
	}
	if gotAlias != "policy-login" {
		t.Fatalf("hook saw alias %q, want policy-login", gotAlias)
	}
	// The client must fail at the login stage itself (failed Ack), not later.
	assertAbsentServerAgent(t, server, "policy-login")
}

func TestPolicyControlHookRejectsServeTopology(t *testing.T) {
	var gotInbound string
	var gotLocal, gotRemote string
	link, server := startPolicyServer(t, &Policy{
		OnControl: func(login *message.Login, local, remote *core.URL, inboundSide string) error {
			gotInbound = inboundSide
			if local != nil {
				gotLocal = local.String()
			}
			if remote != nil {
				gotRemote = remote.String()
			}
			return fmt.Errorf("topology not allowed")
		},
	})

	client := newPolicyClient(t, link, "policy-control", "-l socks5://127.0.0.1:0")
	err := dialClientUntil(t, client, 15*time.Second)
	if err == nil {
		t.Fatal("control accepted although the policy refuses the topology")
	}
	if gotInbound != core.SideLocal {
		t.Fatalf("hook saw inbound side %q, want local", gotInbound)
	}
	if !strings.Contains(gotLocal, "socks5://") {
		t.Fatalf("hook saw local %q, want the client's socks5 serve", gotLocal)
	}
	if gotRemote != "" && !strings.Contains(gotRemote, "raw://") {
		t.Fatalf("hook saw remote %q, want the defaulted raw outbound", gotRemote)
	}
	assertAbsentServerAgent(t, server, "policy-control")
}

// newPolicyClient builds an in-process client console for the rejection
// tests, where the handshake is expected to fail before any registration.
func newPolicyClient(t *testing.T, link, alias, serves string) *Console {
	t.Helper()
	cmdline := fmt.Sprintf("-c %s -k %s -q -a %s %s", link, policyTestKey, alias, serves)
	con, err := NewConsoleWithCMD(cmdline)
	if err != nil {
		t.Fatalf("client %s: NewConsoleWithCMD: %v", alias, err)
	}
	t.Cleanup(func() { _ = con.Close() })
	return con
}

func TestPolicyDialFenceFencesServerOutbound(t *testing.T) {
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	echoAddr := echo.Addr().String()
	go func() {
		for {
			conn, err := echo.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				io.Copy(c, c) //nolint:errcheck // echo
				c.Close()     //nolint:errcheck
			}(conn)
		}
	}()
	defer echo.Close() //nolint:errcheck

	run := func(t *testing.T, fence func(network, addr string) error, expectEcho bool) {
		t.Helper()
		listen := policyFreePort(t)
		link, _ := startPolicyServer(t, &Policy{OnDial: fence})

		// The client serves the inbound port listener; the server dials the
		// echo target on the client's behalf, and the dial policy decides
		// whether that leg lives.
		startPolicyClient(t, link, fmt.Sprintf("policy-dial-%d", listen),
			fmt.Sprintf("-l port://127.0.0.1:%d -r raw://%s", listen, echoAddr))

		deadline := time.Now().Add(25 * time.Second)
		var lastErr error
		for time.Now().Before(deadline) {
			conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", listen), time.Second)
			if err != nil {
				lastErr = err
				time.Sleep(200 * time.Millisecond)
				continue
			}
			conn.SetDeadline(time.Now().Add(5 * time.Second))
			_, _ = conn.Write([]byte("ping"))
			buf := make([]byte, 4)
			n, rerr := io.ReadFull(conn, buf)
			_ = conn.Close()
			if rerr != nil {
				if !expectEcho {
					return // the fence closed the bridged connection, as intended
				}
				t.Fatalf("echo through the tunnel failed: %v", rerr)
			}
			if !expectEcho {
				t.Fatalf("dial policy refused the leg but the echo still answered: %q", buf[:n])
			}
			if string(buf[:n]) != "ping" {
				t.Fatalf("echo corrupted: %q", buf[:n])
			}
			return
		}
		t.Fatalf("no outcome within the deadline (last error: %v)", lastErr)
	}

	t.Run("refused", func(t *testing.T) {
		run(t, func(network, addr string) error { return fmt.Errorf("no egress for %s %s", network, addr) }, false)
	})
	t.Run("allowed", func(t *testing.T) {
		run(t, func(network, addr string) error {
			if strings.HasPrefix(addr, "127.0.0.1:") {
				return nil
			}
			return fmt.Errorf("addr %s outside the fence", addr)
		}, true)
	})
}

// Ensure the os import stays justified even if the helper changes.
