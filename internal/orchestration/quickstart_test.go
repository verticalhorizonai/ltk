package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stellar/go-stellar-sdk/network"
)

const (
	// testReadiness is the injected readiness deadline of the tests that expect readiness: far above the few
	// milliseconds a local stub answers in, for a loaded shared runner.
	testReadiness = 30 * time.Second
	// testPoll is the injected pause between probes.
	testPoll = 10 * time.Millisecond
)

// rpcStubConfig sets what the stub of quickstart's front door answers.
type rpcStubConfig struct {
	// unhealthyFor is how many getHealth calls answer "starting" before "healthy".
	unhealthyFor int32
	// protocol is what getNetwork reports once lowProtocolFor calls are spent; lowProtocolFor calls report protocol-1.
	protocol       int
	lowProtocolFor int32
	// passphrase is what getNetwork reports; "" is the standalone network's.
	passphrase string
	// friendbotDownFor is how many friendbot probes answer 502 before 400.
	friendbotDownFor int32
}

// rpcStub is an httptest server standing in for quickstart's /rpc and /friendbot.
type rpcStub struct {
	*httptest.Server
	health, net, fb atomic.Int32
}

func newRPCStub(t *testing.T, cfg rpcStubConfig) *rpcStub {
	t.Helper()
	if cfg.passphrase == "" {
		cfg.passphrase = StandalonePassphrase
	}
	s := &rpcStub{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/friendbot" {
			if s.fb.Add(1) <= cfg.friendbotDownFor {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"detail":"missing addr"}`))
			return
		}
		var call struct {
			Method string `json:"method"`
		}
		_ = json.NewDecoder(req.Body).Decode(&call)
		var result any
		switch call.Method {
		case "getHealth":
			status := "healthy"
			if s.health.Add(1) <= cfg.unhealthyFor {
				status = "starting"
			}
			result = map[string]any{"status": status}
		case "getNetwork":
			p := cfg.protocol
			if s.net.Add(1) <= cfg.lowProtocolFor {
				p--
			}
			result = map[string]any{"passphrase": cfg.passphrase, "protocolVersion": p}
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "error": map[string]any{"code": -32601, "message": "method not found"}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": result})
	}))
	t.Cleanup(s.Close)
	return s
}

// hostPort is the stub's 127.0.0.1:<port>, what `docker port` would print.
func (s *rpcStub) hostPort() string { return strings.TrimPrefix(s.URL, "http://") }

// The launcher creates its own labelled network and runs the pinned quickstart on it in local mode
// with core and RPC only, port 8000 published on 127.0.0.1 only, and no --protocol-version for the image's default.
func TestStartQuickstartArguments(t *testing.T) {
	srv := newRPCStub(t, rpcStubConfig{protocol: CurrentProtocol})
	f := newFake(t, fakeRule{Prefix: []string{"port"}, Stdout: srv.hostPort() + "\n"})
	r := f.runner("0badcafe")
	n, err := r.startQuickstart(context.Background(), QuickstartCurrent, CurrentProtocol, testReadiness, testPoll)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"network create --label ltk.managed=true --label ltk.run=0badcafe ltk-0badcafe-net",
		"run -d --label ltk.managed=true --label ltk.run=0badcafe --name ltk-0badcafe-core --network ltk-0badcafe-net -p 127.0.0.1::8000 " +
			QuickstartCurrent + " --local --enable core,rpc",
		"port ltk-0badcafe-core 8000/tcp",
	}
	if got := f.argLines(); !slices.Equal(got, want) {
		t.Fatalf("calls:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	wantNet := Network{
		Name: "ltk-0badcafe-net", Container: "ltk-0badcafe-core", Image: QuickstartCurrent,
		Protocol: CurrentProtocol, Passphrase: StandalonePassphrase,
		RPCURL: srv.URL + "/rpc", FriendbotURL: srv.URL + "/friendbot",
		InNetworkRPCURL: "http://ltk-0badcafe-core:8000/rpc", InNetworkFriendbotURL: "http://ltk-0badcafe-core:8000/friendbot",
	}
	if *n != wantNet {
		t.Fatalf("network %+v\nwant %+v", *n, wantNet)
	}
}

// The protocol-28 image runs at 28 with no flag; a protocol the image does not reach by itself
// gets --protocol-version.
func TestQuickstartProtocolFlagOnlyWhenNeeded(t *testing.T) {
	cases := []struct {
		image    string
		protocol int
		want     []string
	}{
		{QuickstartCurrent, CurrentProtocol, []string{"--local", "--enable", "core,rpc"}},
		{QuickstartProtocol28, FallbackProtocol, []string{"--local", "--enable", "core,rpc"}},
		{QuickstartCurrent, 29, []string{"--local", "--enable", "core,rpc", "--protocol-version", "29"}},
	}
	for _, c := range cases {
		if got := quickstartArgs(c.image, c.protocol); !slices.Equal(got, c.want) {
			t.Errorf("quickstartArgs(%s, %d) = %q; want %q", c.image, c.protocol, got, c.want)
		}
	}
}

// Readiness waits for healthy, then for the requested protocol (the current image upgrades to it after
// RPC is healthy), then for the friendbot.
func TestReadinessWaitsForHealthProtocolAndFriendbot(t *testing.T) {
	srv := newRPCStub(t, rpcStubConfig{protocol: CurrentProtocol, unhealthyFor: 3, lowProtocolFor: 2, friendbotDownFor: 2})
	f := newFake(t, fakeRule{Prefix: []string{"port"}, Stdout: srv.hostPort() + "\n"},
		fakeRule{Prefix: []string{"inspect"}, Stdout: "running\n"})
	n, err := f.runner("0badcafe").startQuickstart(context.Background(), QuickstartCurrent, CurrentProtocol, testReadiness, testPoll)
	if err != nil {
		t.Fatal(err)
	}
	if n.Protocol != CurrentProtocol {
		t.Fatalf("protocol %d", n.Protocol)
	}
	if srv.health.Load() < 4 || srv.net.Load() < 3 || srv.fb.Load() < 3 {
		t.Fatalf("probes: getHealth %d, getNetwork %d, friendbot %d; readiness did not wait for each stage",
			srv.health.Load(), srv.net.Load(), srv.fb.Load())
	}
}

// A network that never becomes ready ends, at the injected deadline, in a *ReadinessError that carries the
// core container's last log lines.
func TestReadinessTimeoutCarriesLogs(t *testing.T) {
	srv := newRPCStub(t, rpcStubConfig{protocol: CurrentProtocol, unhealthyFor: 1 << 30})
	f := newFake(t, fakeRule{Prefix: []string{"port"}, Stdout: srv.hostPort() + "\n"},
		fakeRule{Prefix: []string{"inspect"}, Stdout: "running\n"},
		fakeRule{Prefix: []string{"logs"}, Stdout: "core: starting\nrpc: waiting for core\n"})
	_, err := f.runner("0badcafe").startQuickstart(context.Background(), QuickstartCurrent, CurrentProtocol, 200*time.Millisecond, testPoll)
	var re *ReadinessError
	if !errors.As(err, &re) {
		t.Fatalf("err = %v; want a *ReadinessError", err)
	}
	if re.Stage != "RPC healthy" || !strings.Contains(re.Logs, "rpc: waiting for core") || !strings.Contains(err.Error(), "rpc: waiting for core") {
		t.Fatalf("readiness error: stage %q, logs %q, text %q", re.Stage, re.Logs, err.Error())
	}
	if !slices.Contains(f.argLines(), "logs --tail 40 ltk-0badcafe-core") {
		t.Fatalf("no `docker logs --tail 40` call: %q", f.argLines())
	}
}

// A passphrase other than the standalone network's ends the wait at once; the tool never runs against a public
// network.
func TestReadinessRefusesAnotherPassphrase(t *testing.T) {
	srv := newRPCStub(t, rpcStubConfig{protocol: CurrentProtocol, passphrase: network.TestNetworkPassphrase})
	f := newFake(t, fakeRule{Prefix: []string{"port"}, Stdout: srv.hostPort() + "\n"})
	_, err := f.runner("0badcafe").startQuickstart(context.Background(), QuickstartCurrent, CurrentProtocol, testReadiness, testPoll)
	var re *ReadinessError
	if !errors.As(err, &re) || !strings.Contains(err.Error(), network.TestNetworkPassphrase) {
		t.Fatalf("err = %v", err)
	}
}

// A quickstart container that stops while starting ends the wait at once, with its log.
func TestReadinessStopsWhenTheContainerExits(t *testing.T) {
	srv := newRPCStub(t, rpcStubConfig{protocol: CurrentProtocol, unhealthyFor: 1 << 30})
	f := newFake(t, fakeRule{Prefix: []string{"port"}, Stdout: srv.hostPort() + "\n"},
		fakeRule{Prefix: []string{"inspect"}, Stdout: "exited\n"},
		fakeRule{Prefix: []string{"logs"}, Stdout: "Unknown container arg\n"})
	_, err := f.runner("0badcafe").startQuickstart(context.Background(), QuickstartCurrent, CurrentProtocol, testReadiness, testPoll)
	var re *ReadinessError
	if !errors.As(err, &re) || re.Stage != "the container to keep running" || !strings.Contains(re.Logs, "Unknown container arg") {
		t.Fatalf("err = %v", err)
	}
}

// StopQuickstart removes the container with its anonymous volumes, then the network.
func TestStopQuickstart(t *testing.T) {
	f := newFake(t)
	r := f.runner("0badcafe")
	if err := r.StopQuickstart(context.Background(), &Network{Name: "ltk-0badcafe-net", Container: "ltk-0badcafe-core"}); err != nil {
		t.Fatal(err)
	}
	want := []string{"rm -f -v ltk-0badcafe-core", "network rm ltk-0badcafe-net"}
	if got := f.argLines(); !slices.Equal(got, want) {
		t.Fatalf("calls %q; want %q", got, want)
	}
}
