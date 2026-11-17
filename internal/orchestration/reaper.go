package orchestration

// The Resource reaper and the clean-stop helpers. Teardown is label-driven: the reaper removes what carries this run's
// ltk.run label, whatever the process remembers. Snapshot and Leftovers compare every container, network and volume
// before and after a run, so an anonymous volume, which no label filter sees, is still counted.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// TeardownTimeout bounds the reaper: long enough for `docker rm -f` of a quickstart and two TimescaleDB containers on a
// loaded machine.
const TeardownTimeout = 2 * time.Minute

// Reap removes every resource of this run, in this order: containers (`docker rm -f -v`, so their anonymous volumes
// go with them), then networks, then volumes, each selected by `label=ltk.run=<runID>`. It runs under
// its own TeardownTimeout on a context that ctx's cancellation does not reach, so the interrupt that cancelled the work
// cannot cancel the cleanup. Every failure is returned, joined, naming the resource; the caller joins it behind the
// command's primary error.
func (r *Runner) Reap(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), TeardownTimeout)
	defer cancel()
	var errs []error
	for _, kind := range []struct {
		list   []string
		remove []string
		what   string
	}{
		{[]string{"ps", "-a", "-q", "--no-trunc", "--filter", r.runFilter()}, []string{"rm", "-f", "-v"}, "containers"},
		{[]string{"network", "ls", "-q", "--no-trunc", "--filter", r.runFilter()}, []string{"network", "rm"}, "networks"},
		{[]string{"volume", "ls", "-q", "--filter", r.runFilter()}, []string{"volume", "rm"}, "volumes"},
	} {
		out, _, err := r.call(ctx, nil, kind.list...)
		if err != nil {
			errs = append(errs, fmt.Errorf("listing the run's %s: %w", kind.what, err))
			continue
		}
		ids := strings.Fields(string(out))
		if len(ids) == 0 {
			continue
		}
		if _, _, err := r.call(ctx, nil, append(kind.remove, ids...)...); err != nil {
			errs = append(errs, fmt.Errorf("removing the run's %s %s: %w", kind.what, strings.Join(ids, " "), err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("teardown of run %s left resources: %w", r.runID, errors.Join(errs...))
	}
	return nil
}

// Snapshot is the set of every container (running or stopped), network and volume of the Docker engine at one moment,
// each keyed by its id (a volume by its name) with a readable description.
type Snapshot struct {
	// Containers maps each container's full id to "container <name>".
	Containers map[string]string
	// Networks maps each network's full id to "network <name>".
	Networks map[string]string
	// Volumes maps each volume's name to "volume <name>".
	Volumes map[string]string
}

// Snapshot records every container, network and volume of the engine, not only the labelled ones: a TimescaleDB
// container's anonymous volume carries no label. The command handlers take one before their first create call and
// one after the reaper; the integration tests do the same.
func (r *Runner) Snapshot(ctx context.Context) (Snapshot, error) {
	s := Snapshot{}
	var err error
	if s.Containers, err = r.listAll(ctx, "container", "ps", "-a", "--no-trunc", "--format", "{{.ID}}\t{{.Names}}"); err != nil {
		return Snapshot{}, err
	}
	if s.Networks, err = r.listAll(ctx, "network", "network", "ls", "--no-trunc", "--format", "{{.ID}}\t{{.Name}}"); err != nil {
		return Snapshot{}, err
	}
	if s.Volumes, err = r.listAll(ctx, "volume", "volume", "ls", "--format", "{{.Name}}\t{{.Name}}"); err != nil {
		return Snapshot{}, err
	}
	return s, nil
}

// listAll runs a listing whose lines are "<key>\t<name>" and returns key → "<what> <name>".
func (r *Runner) listAll(ctx context.Context, what string, args ...string) (map[string]string, error) {
	out, _, err := r.call(ctx, nil, args...)
	if err != nil {
		return nil, fmt.Errorf("listing every %s: %w", what, err)
	}
	m := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, name, _ := strings.Cut(line, "\t")
		if name == "" {
			name = key
		}
		m[key] = what + " " + name
	}
	return m, nil
}

// Leftovers returns, sorted, every container, network and volume present in after and absent from before: what a run
// left behind. A resource present in both (another project's container) is not counted. An empty result is a clean
// stop.
func Leftovers(before, after Snapshot) []string {
	var out []string
	for _, pair := range [][2]map[string]string{
		{before.Containers, after.Containers},
		{before.Networks, after.Networks},
		{before.Volumes, after.Volumes},
	} {
		for key, desc := range pair[1] {
			if _, existed := pair[0][key]; !existed {
				out = append(out, desc)
			}
		}
	}
	sort.Strings(out)
	return out
}
