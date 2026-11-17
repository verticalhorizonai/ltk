package main

// The Environment check verification test, with the images each command needs: the check called directly with the fake
// docker, on every CI system without a container engine.

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/verticalhorizonai/ltk/internal/contract"
	"github.com/verticalhorizonai/ltk/internal/model"
	"github.com/verticalhorizonai/ltk/internal/orchestration"
)

// envCode is the ErrorCode a check error carries, "" when it carries none.
func envCode(err error) contract.ErrorCode {
	var coded *model.CodedError
	if errors.As(err, &coded) {
		return coded.Code
	}
	return ""
}

// TestEnvironmentCheckConditions: the check fails fast. Each of its three conditions (no docker CLI, no daemon, not
// Linux containers) is an environment error (exit 3) whose message names what is missing, and the fake is called with
// nothing but `version`. A daemon that does not answer within the check's deadline is condition 2 (the deadline is
// injected, not waited for).
func TestEnvironmentCheckConditions(t *testing.T) {
	cases := []struct {
		name, modes string
		noDocker    bool
		timeout     time.Duration
		want        string
		calls       []string
	}{
		{"1 docker CLI not on PATH", "", true, EnvCheckTimeout,
			"Docker with Linux containers is not available: docker CLI not found on PATH", nil},
		{"2 daemon not reachable", "no-daemon", false, EnvCheckTimeout,
			"Docker with Linux containers is not available: Docker daemon not reachable: Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?",
			[]string{"version"}},
		{"2 daemon does not answer in time", "hang-version", false, 200 * time.Millisecond,
			"Docker with Linux containers is not available: Docker daemon not reachable: `docker version` gave no answer within 200ms",
			[]string{"version"}},
		{"3 not Linux containers", "windows", false, EnvCheckTimeout,
			`Docker with Linux containers is not available: Docker is not running Linux containers (its daemon runs "windows" containers)`,
			[]string{"version"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var f *fakeEngine
			if c.noDocker { // no fake needed: this row runs on every system
				t.Setenv("PATH", t.TempDir())
			} else {
				f = newFakeEngine(t, c.modes, "")
				f.inProcess()
			}
			r, err := checkEnvironment(context.Background(), "0a1b2c3d", orchestration.CheckImages, c.timeout)
			if r != nil || envCode(err) != contract.ErrorCodeEnvironmentMissing {
				t.Fatalf("runner %v, error %v: want an environment error", r, err)
			}
			if err.Error() != c.want {
				t.Errorf("message %q\nwant    %q", err.Error(), c.want)
			}
			if f == nil {
				return
			}
			if got := f.subcommands(); !slices.Equal(got, c.calls) {
				t.Errorf("the fake was called with %v, want %v: nothing but version", got, c.calls)
			}
		})
	}
}

// TestEnvironmentCheckImages: with Docker running Linux containers, each
// image of the command's set is inspected; an absent one is pulled by its digest; a pull that fails is an environment
// error naming the image. Nothing is created.
func TestEnvironmentCheckImages(t *testing.T) {
	t.Run("present", func(t *testing.T) {
		f := newFakeEngine(t, "", "")
		f.inProcess()
		r, err := checkEnvironment(context.Background(), "0a1b2c3d", orchestration.GenerateImages, EnvCheckTimeout)
		if err != nil || r == nil {
			t.Fatalf("%v", err)
		}
		if got := f.subcommands(); !slices.Equal(got, []string{"version", "image inspect", "image inspect"}) {
			t.Errorf("calls %v", got)
		}
		if got := f.calls(); got[1][len(got[1])-1] != orchestration.QuickstartCurrent || got[2][len(got[2])-1] != orchestration.QuickstartProtocol28 {
			t.Errorf("the images inspected: %v", got)
		}
	})
	t.Run("absent and pulled", func(t *testing.T) {
		f := newFakeEngine(t, "pull-ok", "")
		f.inProcess()
		if _, err := checkEnvironment(context.Background(), "0a1b2c3d", orchestration.CheckImages, EnvCheckTimeout); err != nil {
			t.Fatal(err)
		}
		calls := f.calls()
		if got := f.subcommands(); !slices.Equal(got, []string{"version", "image inspect", "pull"}) || !slices.Equal(calls[2], []string{"pull", orchestration.QuickstartCurrent}) {
			t.Errorf("calls %v: the absent image is pulled by its digest", calls)
		}
	})
	t.Run("absent and unpullable", func(t *testing.T) {
		f := newFakeEngine(t, "no-image", "")
		f.inProcess()
		_, err := checkEnvironment(context.Background(), "0a1b2c3d", orchestration.CheckImages, EnvCheckTimeout)
		if envCode(err) != contract.ErrorCodeEnvironmentMissing {
			t.Fatalf("%v: want an environment error", err)
		}
		if !strings.HasPrefix(err.Error(), "digest-pinned image cannot be pulled: "+orchestration.QuickstartCurrent+" (") {
			t.Errorf("message %q: want the environment error's wording naming the image", err)
		}
		if got := f.subcommands(); !slices.Equal(got, []string{"version", "image inspect", "pull"}) {
			t.Errorf("calls %v", got)
		}
	})
}

// TestEnvironmentCheckPasses: where Docker runs Linux containers and the images are present the check passes and
// returns the run's runner; an interrupt during the check is returned as it is, not as a missing environment.
func TestEnvironmentCheckPasses(t *testing.T) {
	f := newFakeEngine(t, "", "")
	f.inProcess()
	r, err := checkEnvironment(context.Background(), "0a1b2c3d", orchestration.CheckImages, EnvCheckTimeout)
	if err != nil || r == nil || r.Name("xdr") != "ltk-0a1b2c3d-xdr" {
		t.Fatalf("%v %v", r, err)
	}
	g := newFakeEngine(t, "hang-version", "")
	g.inProcess()
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for len(g.calls()) == 0 {
			time.Sleep(10 * time.Millisecond)
		}
		cancel()
	}()
	_, err = checkEnvironment(ctx, "0a1b2c3d", orchestration.CheckImages, time.Hour)
	if !errors.Is(err, context.Canceled) || envCode(err) != "" {
		t.Errorf("an interrupted check: %v, want the cancellation and no environment error", err)
	}
}
