package main

// The Interrupt handler, in process with the fake docker: the teardown runs on success, failure, interrupt and panic,
// and a teardown that leaves something is a failure naming it. The process-level interrupt is in check_test.go.

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/verticalhorizonai/ltk/internal/contract"
	"github.com/verticalhorizonai/ltk/internal/model"
	"github.com/verticalhorizonai/ltk/internal/orchestration"
)

// startTool is work that starts the run's Rust XDR tool container, then returns ret.
func startTool(ret error) func(context.Context, *orchestration.Runner) error {
	return func(ctx context.Context, r *orchestration.Runner) error {
		if _, err := r.StartXDRTool(ctx); err != nil {
			return err
		}
		return ret
	}
}

// anyRun is work that does not need the run's id, as runWithTeardown takes it.
func anyRun(work func(context.Context, *orchestration.Runner) error) runWork {
	return func(ctx context.Context, r *orchestration.Runner, _ string) error { return work(ctx, r) }
}

// runName returns the container name of the fake's `run` call, and checks the run carried both labels.
func runName(t *testing.T, f *fakeEngine) string {
	t.Helper()
	for _, c := range f.calls() {
		if len(c) > 0 && c[0] == "run" {
			i := slices.Index(c, "--name")
			if i < 0 || !slices.Contains(c, "ltk.managed=true") {
				t.Fatalf("run without its name or labels: %v", c)
			}
			return c[i+1]
		}
	}
	t.Fatal("nothing was run")
	return ""
}

// reapedAfterWork asserts the order: the snapshot before the first create, the create, then `rm -f -v`, then the
// snapshot after it.
func reapedAfterWork(t *testing.T, f *fakeEngine) {
	t.Helper()
	got := f.subcommands()
	run, rm := slices.Index(got, "run"), slices.Index(got, "rm")
	firstPs, lastPs := slices.Index(got, "ps"), -1
	for i, c := range got {
		if c == "ps" {
			lastPs = i
		}
	}
	if run < 0 || rm < run || firstPs > run || lastPs < rm {
		t.Errorf("calls %v: want a snapshot, the create, the removal, a snapshot", got)
	}
	for _, c := range f.calls() {
		if len(c) > 0 && c[0] == "rm" && !slices.Equal(c[:3], []string{"rm", "-f", "-v"}) {
			t.Errorf("removal %v: want rm -f -v (the container and its anonymous volumes)", c)
		}
	}
}

// TestInterruptHandlerTearsDown: on success, on a failure, on an interrupt and on a panic, the run's container is
// removed by its label, and only the work's outcome decides the exit.
func TestInterruptHandlerTearsDown(t *testing.T) {
	other := fakeContainer{ID: "otherproject1", Name: "notion-db", Labels: map[string]string{}}
	cases := []struct {
		name  string
		work  func(context.Context, *orchestration.Runner) error
		check func(t *testing.T, err error)
	}{
		{"success", startTool(nil), func(t *testing.T, err error) {
			if err != nil {
				t.Errorf("%v", err)
			}
		}},
		{"failure", startTool(model.Failure("2 of 3 entries failed")), func(t *testing.T, err error) {
			if envCode(err) != contract.ErrorCodeFailure || err.Error() != "2 of 3 entries failed" {
				t.Errorf("%v: the work's error, unchanged", err)
			}
		}},
		{"interrupt", nil, func(t *testing.T, err error) {
			if !errors.Is(err, context.Canceled) {
				t.Errorf("%v: the interrupt is the outcome", err)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFakeEngine(t, "", "")
			f.inProcess()
			f.seed(other)
			parent, interrupt := context.WithCancel(context.Background())
			defer interrupt()
			work := c.work
			if work == nil { // the interrupt arrives while the work runs: the command's own context is cancelled
				work = func(ctx context.Context, r *orchestration.Runner) error {
					if _, err := r.StartXDRTool(ctx); err != nil {
						return err
					}
					interrupt()
					<-ctx.Done()
					return ctx.Err()
				}
			}
			err := runWithTeardown(parent, orchestration.CheckImages, EnvCheckTimeout, anyRun(work))
			c.check(t, err)
			reapedAfterWork(t, f)
			if got := f.containers(); len(got) != 1 || got[0].ID != other.ID {
				t.Errorf("containers after the run %v: only the other project's is left", got)
			}
		})
	}
	t.Run("panic", func(t *testing.T) {
		f := newFakeEngine(t, "", "")
		f.inProcess()
		defer func() {
			if p := recover(); p != "boom" {
				t.Errorf("recovered %v: the panic goes on after the teardown", p)
			}
			reapedAfterWork(t, f)
			if got := f.containers(); len(got) != 0 {
				t.Errorf("containers after the panic %v", got)
			}
		}()
		_ = runWithTeardown(context.Background(), orchestration.CheckImages, EnvCheckTimeout, func(ctx context.Context, r *orchestration.Runner, _ string) error {
			if _, err := r.StartXDRTool(ctx); err != nil {
				return err
			}
			panic("boom")
		})
	})
}

// TestInterruptHandlerLeftovers: a teardown that leaves the run's container makes a run that succeeded fail (exit 1)
// naming it; after a failed run the leftover is joined behind the primary error, whose code the mapper keeps. A
// container another program started during the run is not the run's and is not counted.
func TestInterruptHandlerLeftovers(t *testing.T) {
	t.Run("after success", func(t *testing.T) {
		f := newFakeEngine(t, "rm-fails", "")
		f.inProcess()
		err := runWithTeardown(context.Background(), orchestration.CheckImages, EnvCheckTimeout, anyRun(startTool(nil)))
		name := runName(t, f)
		if envCode(err) != contract.ErrorCodeFailure || !strings.Contains(err.Error(), "left container "+name) {
			t.Errorf("%v: want a failure naming container %s", err, name)
		}
	})
	t.Run("after a failure", func(t *testing.T) {
		f := newFakeEngine(t, "rm-fails", "")
		f.inProcess()
		err := runWithTeardown(context.Background(), orchestration.CheckImages, EnvCheckTimeout, anyRun(startTool(model.Usage("primary"))))
		if envCode(err) != contract.ErrorCodeUsageError || !strings.HasPrefix(err.Error(), "primary; ") || !strings.Contains(err.Error(), "left container "+runName(t, f)) {
			t.Errorf("%v: the primary error first, its code kept, the leftover behind it", err)
		}
	})
	t.Run("another program's container", func(t *testing.T) {
		f := newFakeEngine(t, "", "")
		f.inProcess()
		err := runWithTeardown(context.Background(), orchestration.CheckImages, EnvCheckTimeout, func(ctx context.Context, r *orchestration.Runner, _ string) error {
			f.seed(fakeContainer{ID: "otherproject2", Name: "notion-web", Labels: map[string]string{}})
			return nil
		})
		if err != nil {
			t.Errorf("%v: a container that is not the run's is not a leftover", err)
		}
	})
}

// TestInterruptHandlerEnvironmentFirst: when the environment check fails the work does not run, no snapshot is taken
// and nothing is created or removed.
func TestInterruptHandlerEnvironmentFirst(t *testing.T) {
	f := newFakeEngine(t, "windows", "")
	f.inProcess()
	ran := false
	err := runWithTeardown(context.Background(), orchestration.CheckImages, EnvCheckTimeout, func(context.Context, *orchestration.Runner, string) error {
		ran = true
		return nil
	})
	if ran || envCode(err) != contract.ErrorCodeEnvironmentMissing || !slices.Equal(f.subcommands(), []string{"version"}) {
		t.Errorf("ran %v, error %v, calls %v", ran, err, f.subcommands())
	}
}
