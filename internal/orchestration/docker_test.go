package orchestration

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/verticalhorizonai/ltk/internal/contract"
	"github.com/verticalhorizonai/ltk/internal/model"
)

// The run id is 8 hex characters from crypto/rand, fresh per invocation.
func TestNewRunIDIsEightHexCharacters(t *testing.T) {
	re := regexp.MustCompile(`^[0-9a-f]{8}$`)
	seen := map[string]bool{}
	for i := 0; i < 20; i++ {
		id, err := NewRunID()
		if err != nil {
			t.Fatal(err)
		}
		if !re.MatchString(id) {
			t.Fatalf("run id %q is not 8 lowercase hex characters", id)
		}
		seen[id] = true
	}
	if len(seen) < 19 {
		t.Fatalf("20 run ids gave only %d distinct values", len(seen))
	}
}

// No docker on PATH is an environment error (exit 3) saying so.
func TestNewRunnerWithoutDockerIsEnvironmentError(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, err := NewRunner("0123abcd")
	var coded *model.CodedError
	if !errors.As(err, &coded) || coded.Code != contract.ErrorCodeEnvironmentMissing {
		t.Fatalf("NewRunner with no docker: %v; want an environment_missing error", err)
	}
	if coded.Message != "docker CLI not found on PATH" {
		t.Fatalf("message %q", coded.Message)
	}
}

func TestNewRunnerRejectsMalformedRunID(t *testing.T) {
	for _, id := range []string{"", "0123abc", "0123abcde", "0123abcg"} {
		if _, err := NewRunner(id); err == nil {
			t.Errorf("NewRunner(%q) accepted a malformed run id", id)
		}
	}
}

// Every create call carries ltk.managed=true and ltk.run=<runID>, and every name is ltk-<runID>-<role>; a published
// port is 127.0.0.1-bound only.
func TestEveryCreateCarriesBothLabelsAndTheRunName(t *testing.T) {
	srv := newRPCStub(t, rpcStubConfig{protocol: CurrentProtocol})
	f := newFake(t, fakeRule{Prefix: []string{"port"}, Stdout: srv.hostPort() + "\n"},
		fakeRule{Prefix: []string{"inspect"}, Stdout: "running\n"})
	r := f.runner("a1b2c3d4")
	ctx := context.Background()
	if _, err := r.CreateNetwork(ctx, "extra"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Run(ctx, ContainerSpec{Role: "db", Image: TimescaleDBImage, Network: "ltk-a1b2c3d4-net", Env: []string{"A=b"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.startQuickstart(ctx, QuickstartCurrent, CurrentProtocol, testReadiness, testPoll); err != nil {
		t.Fatal(err)
	}
	if _, err := r.StartXDRTool(ctx); err != nil {
		t.Fatal(err)
	}
	creates := 0
	nameRE := regexp.MustCompile(`^ltk-a1b2c3d4-[a-z0-9-]+$`)
	for _, c := range f.calls() {
		if !isCreate(c.Args) {
			continue
		}
		creates++
		labels := flagValues(c.Args, "--label")
		if !slices.Contains(labels, "ltk.managed=true") || !slices.Contains(labels, "ltk.run=a1b2c3d4") {
			t.Errorf("create call %q lacks a label: %q", c.Args, labels)
		}
		var name string
		if c.Args[0] == "run" {
			names := flagValues(c.Args, "--name")
			if len(names) != 1 {
				t.Errorf("run call %q has no single --name", c.Args)
				continue
			}
			name = names[0]
			for _, p := range flagValues(c.Args, "-p") {
				if p != "127.0.0.1::8000" {
					t.Errorf("run call %q publishes %q, not on 127.0.0.1 only", c.Args, p)
				}
			}
		} else {
			name = c.Args[len(c.Args)-1]
		}
		if !nameRE.MatchString(name) {
			t.Errorf("create call %q names %q, not ltk-<runID>-<role>", c.Args, name)
		}
	}
	if creates != 5 { // two networks, the db, the quickstart, the XDR tool
		t.Fatalf("%d create calls; want 5:\n%q", creates, f.argLines())
	}
}

// A failed docker call is a *CommandError carrying docker's exit code and standard error.
func TestFailedCallCarriesStderrAndExitCode(t *testing.T) {
	f := newFake(t, fakeRule{Prefix: []string{"network", "create"}, Stderr: "network with name exists\n", Exit: 1})
	r := f.runner("a1b2c3d4")
	_, err := r.CreateNetwork(context.Background(), "net")
	var ce *CommandError
	if !errors.As(err, &ce) || ce.ExitCode != 1 || ce.Stderr != "network with name exists" {
		t.Fatalf("CreateNetwork: %#v", err)
	}
}

// An interrupted native run once showed this: a cancelled `docker run` put the whole argument list, a build script
// included, in the ErrorLine. Error() names the subcommand and the resource only, with docker's first standard-error
// line cut to maxDockerMessage bytes (as the Rust XDR tool's message is cut); Args and Stderr keep everything.
func TestCommandErrorNamesSubcommandAndResourceOnly(t *testing.T) {
	script := "set -eu\nstep() { echo \"ltk-build-step: $*\"; }\ngit clone https://github.com/stellar/wallet-backend.git /src"
	runArgs := []string{"run", "-d", "--label", "ltk.managed=true", "--label", "ltk.run=1101ed22", "--name", "ltk-1101ed22-wb-build",
		"-e", "GOSUMDB=sum.golang.org", "-v", "ltk-1101ed22-wb-bin:/out", "--entrypoint", "sh", GoBuildImage, "-c", script}
	cancelled := errors.Join(errors.New("signal: killed"), context.Canceled)
	long := strings.Repeat("é", 400) // 800 bytes, two per rune: the cut must fall on a rune boundary
	for _, c := range []struct {
		name string
		err  *CommandError
		want string
	}{
		{"a cancelled run", &CommandError{Args: runArgs, ExitCode: -1, Err: cancelled}, "docker run ltk-1101ed22-wb-build: signal: killed; context canceled"},
		{"a run docker refused", &CommandError{Args: runArgs, ExitCode: 125, Stderr: "docker: Error response from daemon: Conflict.\nSee 'docker run --help'.", Err: errors.New("exit status 125")},
			"docker run ltk-1101ed22-wb-build: docker: Error response from daemon: Conflict."},
		{"a network create", &CommandError{Args: []string{"network", "create", "--label", "ltk.managed=true", "--label", "ltk.run=a1b2c3d4", "ltk-a1b2c3d4-net"}, Stderr: "network with name exists", Err: errors.New("exit status 1")},
			"docker network create ltk-a1b2c3d4-net: network with name exists"},
		{"an exec", &CommandError{Args: []string{"exec", "-i", "ltk-a1b2c3d4-xdr", "stellar-xdr", "decode", "--type", "LedgerCloseMeta"}, Stderr: "error: bad", Err: errors.New("exit status 1")},
			"docker exec ltk-a1b2c3d4-xdr: error: bad"},
		{"a cp", &CommandError{Args: []string{"cp", "-", "ltk-a1b2c3d4-js:/work"}, Stderr: "no such container", Err: errors.New("exit status 1")}, "docker cp ltk-a1b2c3d4-js: no such container"},
		{"a port", &CommandError{Args: []string{"port", "ltk-a1b2c3d4-core", "8000/tcp"}, Stderr: "no port", Err: errors.New("exit status 1")}, "docker port ltk-a1b2c3d4-core: no port"},
		{"an inspect", &CommandError{Args: []string{"inspect", "--type", "container", "--format", "{{.State.Status}}", "c1"}, Stderr: "No such container: c1", Err: errors.New("exit status 1")},
			"docker inspect c1: No such container: c1"},
		{"a stop", &CommandError{Args: []string{"stop", "-t", "10", "c1"}, Stderr: "boom", Err: errors.New("exit status 1")}, "docker stop c1: boom"},
		{"a logs", &CommandError{Args: []string{"logs", "--tail", "40", "c1"}, Stderr: "boom", Err: errors.New("exit status 1")}, "docker logs c1: boom"},
		{"the reaper's rm", &CommandError{Args: []string{"rm", "-f", "-v", "c1", "c2"}, Stderr: "removal of container c1 is already in progress", Err: errors.New("exit status 1")},
			"docker rm c1 c2: removal of container c1 is already in progress"},
		{"an image pull", &CommandError{Args: []string{"pull", QuickstartCurrent}, Stderr: "manifest unknown", Err: errors.New("exit status 1")}, "docker pull " + QuickstartCurrent + ": manifest unknown"},
		{"an image inspect", &CommandError{Args: []string{"image", "inspect", "--format", "{{.Id}}", NodeImage}, Stderr: "No such image", Err: errors.New("exit status 1")}, "docker image inspect " + NodeImage + ": No such image"},
		{"version", &CommandError{Args: []string{"version", "--format", "{{.Server.Os}}"}, Stderr: "Cannot connect to the Docker daemon", Err: errors.New("exit status 1")}, "docker version: Cannot connect to the Docker daemon"},
		{"a listing", &CommandError{Args: []string{"ps", "-a", "-q", "--filter", "label=ltk.run=a1b2c3d4"}, Stderr: "boom", Err: errors.New("exit status 1")}, "docker ps: boom"},
		{"a long message", &CommandError{Args: []string{"start", "c1"}, Stderr: long + "\nsecond line", Err: errors.New("exit status 1")}, "docker start c1: " + strings.Repeat("é", 150) + "…"},
	} {
		got := c.err.Error()
		if got != c.want {
			t.Errorf("%s: Error() = %q\nwant %q", c.name, got, c.want)
		}
		if strings.Contains(got, "\n") || strings.Contains(got, "GOSUMDB") || strings.Contains(got, "--label") {
			t.Errorf("%s: Error() carries more than the subcommand, the resource and one line: %q", c.name, got)
		}
	}
	// The fields keep the whole call and the whole standard error.
	e := &CommandError{Args: runArgs, Stderr: long + "\nsecond line", Err: errors.New("exit status 1")}
	if !slices.Equal(e.Args, runArgs) || !strings.HasSuffix(e.Stderr, "second line") {
		t.Fatalf("fields %+v", e)
	}
	if !errors.Is(&CommandError{Args: runArgs, Err: cancelled}, context.Canceled) {
		t.Fatal("a cancelled call's error does not unwrap to context.Canceled")
	}
}

// ServerOS is `docker version --format {{.Server.Os}}`, trimmed; a daemon that does not answer is a *CommandError with
// docker's message.
func TestServerOS(t *testing.T) {
	f := newFake(t, fakeRule{Prefix: []string{"version"}, Stdout: "linux\n"})
	os, err := f.runner("a1b2c3d4").ServerOS(context.Background())
	if err != nil || os != "linux" {
		t.Fatalf("ServerOS = %q, %v", os, err)
	}
	if got := f.argLines(); !slices.Equal(got, []string{"version --format {{.Server.Os}}"}) {
		t.Fatalf("calls %q", got)
	}
	f = newFake(t, fakeRule{Prefix: []string{"version"}, Stderr: "Cannot connect to the Docker daemon\n", Exit: 1})
	_, err = f.runner("a1b2c3d4").ServerOS(context.Background())
	var ce *CommandError
	if !errors.As(err, &ce) || ce.Stderr != "Cannot connect to the Docker daemon" {
		t.Fatalf("ServerOS with no daemon: %v", err)
	}
}

// A port bound anywhere but 127.0.0.1 is refused.
func TestPortMustBeLoopback(t *testing.T) {
	for _, out := range []string{"0.0.0.0:32768\n", "127.0.0.1:32768\n[::]:32768\n", ""} {
		f := newFake(t, fakeRule{Prefix: []string{"port"}, Stdout: out})
		if _, err := f.runner("a1b2c3d4").Port(context.Background(), "c", 8000); err == nil {
			t.Errorf("Port accepted %q", out)
		}
	}
	f := newFake(t, fakeRule{Prefix: []string{"port"}, Stdout: "127.0.0.1:32768\n"})
	addr, err := f.runner("a1b2c3d4").Port(context.Background(), "c", 8000)
	if err != nil || addr != "127.0.0.1:32768" {
		t.Fatalf("Port = %q, %v", addr, err)
	}
}
