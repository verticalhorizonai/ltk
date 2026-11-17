package orchestration

// The Quickstart network launcher. It starts Stellar's quickstart image in local mode with core and RPC only, on the
// run's own Docker network, its one published port bound to 127.0.0.1, and returns when RPC is healthy at the requested
// protocol on the standalone network's passphrase.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// StandalonePassphrase is the passphrase of quickstart's local network, the only network the tool runs against. The Go
// SDK's network package names only the public, test and future networks'.
const StandalonePassphrase = "Standalone Network ; February 2017"

const (
	// ReadinessTimeout bounds the wait for a started quickstart to answer healthy at the requested protocol with its
	// friendbot up. Both pinned quickstarts are ready in well under a minute on a developer machine; five minutes
	// covers a cold, loaded machine with a wide margin.
	ReadinessTimeout = 5 * time.Minute
	// readinessPoll is the pause between two readiness probes.
	readinessPoll = time.Second
	// readinessLogLines is how many of the core container's last log lines a readiness error carries.
	readinessLogLines = 40
	// quickstartPort is the port of quickstart's front door, which serves /rpc and /friendbot.
	quickstartPort = 8000
)

// Network is a started quickstart network.
type Network struct {
	// Name is the run's Docker network, ltk-<runID>-net.
	Name string
	// Container is the quickstart container, ltk-<runID>-core.
	Container string
	// Image is the digest-pinned quickstart image it runs.
	Image string
	// Protocol is the protocol version RPC's getNetwork reports, equal to the one requested.
	Protocol int
	// Passphrase is the network passphrase RPC's getNetwork reports: the standalone network's.
	Passphrase string
	// RPCURL is RPC on the host: http://127.0.0.1:<port>/rpc.
	RPCURL string
	// FriendbotURL is the local friendbot on the host: http://127.0.0.1:<port>/friendbot. The recipes fund their
	// fixed-seed accounts through it.
	FriendbotURL string
	// InNetworkRPCURL is RPC for a sibling container on Name: http://<container>:8000/rpc (wallet-backend).
	InNetworkRPCURL string
	// InNetworkFriendbotURL is the friendbot for a sibling container on Name.
	InNetworkFriendbotURL string
}

// ReadinessError is a quickstart that did not become ready before its deadline, or that stopped while starting. It
// carries the last lines of the core container's log; the reaper still removes the container.
type ReadinessError struct {
	// Container is the quickstart container.
	Container string
	// Waited is how long the launcher waited.
	Waited time.Duration
	// Stage is what it was waiting for: "RPC healthy", "protocol N", "friendbot", or "the container to keep running".
	Stage string
	// Last is the last probe's failure.
	Last error
	// Logs are the last lines of the container's log; empty when they could not be read.
	Logs string
}

// Error names the stage, the last probe's failure and the log's last lines.
func (e *ReadinessError) Error() string {
	msg := fmt.Sprintf("quickstart %s was not ready after %s (waiting for %s)", e.Container, e.Waited.Round(time.Second), e.Stage)
	if e.Last != nil {
		msg += ": " + e.Last.Error()
	}
	if logs := strings.TrimSpace(e.Logs); logs != "" {
		msg += "; last log lines:\n" + logs
	}
	return msg
}

// Unwrap returns the last probe's failure.
func (e *ReadinessError) Unwrap() error { return e.Last }

// StartQuickstart creates the run's network ltk-<runID>-net and starts image on it as ltk-<runID>-core, in
// local mode with core and RPC only (`--local --enable core,rpc`), adding `--protocol-version <N>` only when the image
// does not reach protocol by itself, with port 8000 published on an ephemeral port of 127.0.0.1. It returns when RPC
// is healthy, getNetwork reports protocol and the standalone passphrase, and the friendbot answers, or a
// *ReadinessError after ReadinessTimeout. The run's reaper removes both on every path.
//
// One quickstart runs at a time in a run: the protocol-28 fallback network is started after StopQuickstart removed the
// first, so the two names are reused.
func (r *Runner) StartQuickstart(ctx context.Context, image string, protocol int) (*Network, error) {
	return r.startQuickstart(ctx, image, protocol, ReadinessTimeout, readinessPoll)
}

// StopQuickstart removes the quickstart container (`docker rm -f -v`) and then its network, before the run ends: the
// generation network goes before the checks run, and the fallback network starts after it. The
// reaper still removes whatever this leaves.
func (r *Runner) StopQuickstart(ctx context.Context, n *Network) error {
	var errs []error
	if _, _, err := r.call(ctx, nil, "rm", "-f", "-v", n.Container); err != nil {
		errs = append(errs, err)
	}
	if _, _, err := r.call(ctx, nil, "network", "rm", n.Name); err != nil {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return fmt.Errorf("removing the quickstart network %s: %w", n.Name, errors.Join(errs...))
	}
	return nil
}

// quickstartArgs are the container arguments for image to run at protocol: local mode, core and RPC only, and the
// protocol flag only when the image's default is another protocol.
func quickstartArgs(image string, protocol int) []string {
	args := []string{"--local", "--enable", "core,rpc"}
	if def, known := quickstartDefaultProtocol[image]; !known || def != protocol {
		args = append(args, "--protocol-version", fmt.Sprint(protocol))
	}
	return args
}

// startQuickstart is StartQuickstart with the readiness deadline and the poll interval injected, so a test never waits
// for the real deadline.
func (r *Runner) startQuickstart(ctx context.Context, image string, protocol int, timeout, poll time.Duration) (*Network, error) {
	netName, err := r.CreateNetwork(ctx, "net")
	if err != nil {
		return nil, err
	}
	container, err := r.Run(ctx, ContainerSpec{
		Role:    "core",
		Image:   image,
		Network: netName,
		Publish: []int{quickstartPort},
		Args:    quickstartArgs(image, protocol),
	})
	if err != nil {
		return nil, err
	}
	addr, err := r.Port(ctx, container, quickstartPort)
	if err != nil {
		return nil, err
	}
	n := &Network{
		Name:                  netName,
		Container:             container,
		Image:                 image,
		RPCURL:                "http://" + addr + "/rpc",
		FriendbotURL:          "http://" + addr + "/friendbot",
		InNetworkRPCURL:       fmt.Sprintf("http://%s:%d/rpc", container, quickstartPort),
		InNetworkFriendbotURL: fmt.Sprintf("http://%s:%d/friendbot", container, quickstartPort),
	}
	if err := r.awaitReady(ctx, n, protocol, timeout, poll); err != nil {
		return nil, err
	}
	return n, nil
}

// awaitReady polls until RPC is healthy, getNetwork reports protocol on the standalone passphrase and the friendbot
// answers. A passphrase other than the standalone network's is an error at once: the tool never runs
// against a public network.
func (r *Runner) awaitReady(ctx context.Context, n *Network, protocol int, timeout, poll time.Duration) error {
	start := time.Now()
	deadline := start.Add(timeout)
	client := &http.Client{Timeout: 10 * time.Second}
	stage := "RPC healthy"
	var last error
	fail := func(stage string, last error) error {
		// The log is read on a context the caller's cancellation does not reach: it is evidence.
		logCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		logs, _ := r.Logs(logCtx, n.Container, readinessLogLines)
		return &ReadinessError{Container: n.Container, Waited: time.Since(start), Stage: stage, Last: last, Logs: logs}
	}
	for {
		stage, last = r.probe(ctx, client, n, protocol)
		if stage == "" {
			return nil
		}
		var mismatch *passphraseError
		if errors.As(last, &mismatch) {
			return fail(stage, last)
		}
		if st, err := r.state(ctx, n.Container); err == nil && st != "running" {
			return fail("the container to keep running", fmt.Errorf("the container is %s", st))
		}
		if !time.Now().Add(poll).Before(deadline) {
			return fail(stage, last)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(poll):
		}
	}
}

// passphraseError is a network whose passphrase is not the standalone network's.
type passphraseError struct{ got string }

func (e *passphraseError) Error() string {
	return fmt.Sprintf("RPC reports the network passphrase %q, not the standalone network's %q", e.got, StandalonePassphrase)
}

// probe runs one round of the readiness probes and returns "" when the network is ready, or the stage it is waiting
// for and why. It records the protocol and passphrase in n.
func (r *Runner) probe(ctx context.Context, client *http.Client, n *Network, protocol int) (string, error) {
	var health struct {
		Status string `json:"status"`
	}
	if err := rpcCall(ctx, client, n.RPCURL, "getHealth", &health); err != nil {
		return "RPC healthy", err
	}
	if health.Status != "healthy" {
		return "RPC healthy", fmt.Errorf("getHealth status %q", health.Status)
	}
	var info struct {
		Passphrase      string `json:"passphrase"`
		ProtocolVersion int    `json:"protocolVersion"`
	}
	stage := fmt.Sprintf("protocol %d", protocol)
	if err := rpcCall(ctx, client, n.RPCURL, "getNetwork", &info); err != nil {
		return stage, err
	}
	n.Passphrase, n.Protocol = info.Passphrase, info.ProtocolVersion
	if info.Passphrase != StandalonePassphrase {
		return "the standalone network", &passphraseError{got: info.Passphrase}
	}
	if info.ProtocolVersion != protocol {
		return stage, fmt.Errorf("getNetwork reports protocol %d", info.ProtocolVersion)
	}
	if err := friendbotUp(ctx, client, n.FriendbotURL); err != nil {
		return "friendbot", err
	}
	return "", nil
}

// friendbotUp asks the friendbot with no address: a running friendbot answers 400 ("missing addr"), quickstart's front
// door answers 502 while the friendbot is still starting (it starts after the protocol upgrade).
func friendbotUp(ctx context.Context, client *http.Client, url string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode >= 500 {
		return fmt.Errorf("friendbot answers HTTP %d", resp.StatusCode)
	}
	return nil
}

// rpcCall makes one JSON-RPC 2.0 call with no parameters and decodes its result into out. The readiness probe's
// getHealth and getNetwork belong here, not to internal/generate's client.
func rpcCall(ctx context.Context, client *http.Client, url, method string, out any) error {
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: HTTP %d", method, resp.StatusCode)
	}
	var env struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(b, &env); err != nil {
		return fmt.Errorf("%s: %v", method, err)
	}
	if env.Error != nil {
		return fmt.Errorf("%s: JSON-RPC error %d: %s", method, env.Error.Code, env.Error.Message)
	}
	return json.Unmarshal(env.Result, out)
}
