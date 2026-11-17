package orchestration

// The container life-cycle calls of the two reproductions: a labelled volume for the wallet-backend binaries,
// `docker wait` for the build container, `docker inspect` of a container's state, `docker start` of the exited pre-fix
// ingester (its one restart), `docker stop`, `docker rm -f -v`, the full timestamped log of a container, and
// `docker cp -` of a tar stream into a container (the js-sdk script and its package files; no bind mount, so nothing
// depends on the host's file sharing). The volume is created through create, so it carries the two labels,
// ltk.managed=true and ltk.run=<runID>, like every other resource.

import (
	"bufio"
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Mount is one of the run's volumes mounted into a container (ContainerSpec.Mounts).
type Mount struct {
	// Volume is the volume's name, made by CreateVolume.
	Volume string
	// Target is the absolute path inside the container.
	Target string
	// ReadOnly mounts the volume read-only (`:ro`).
	ReadOnly bool
}

// arg is the `-v` value of the mount.
func (m Mount) arg() string {
	a := m.Volume + ":" + m.Target
	if m.ReadOnly {
		a += ":ro"
	}
	return a
}

// CreateVolume creates the run's labelled volume ltk-<runID>-<role> and returns its name: the Go build container's
// output volume at /out. The reaper removes it with the run's other volumes.
func (r *Runner) CreateVolume(ctx context.Context, role string) (string, error) {
	name := r.Name(role)
	if _, err := r.create(ctx, []string{"volume", "create"}, name); err != nil {
		return "", fmt.Errorf("creating the volume %s: %w", name, err)
	}
	return name, nil
}

// ContainerState is a container's state as `docker inspect` reports it.
type ContainerState struct {
	// Status is "created", "running", "exited", ...
	Status string
	// ExitCode is the exit code of the container's last run; 0 while it runs.
	ExitCode int
}

// Exited reports whether the container has stopped: its status is "exited" or "dead".
func (s ContainerState) Exited() bool { return s.Status == "exited" || s.Status == "dead" }

// State returns the container's status and the exit code of its last run (`docker inspect`). The wallet-backend
// runner polls it for the pre-fix ingester's exit after its five failed attempts.
func (r *Runner) State(ctx context.Context, container string) (ContainerState, error) {
	out, _, err := r.call(ctx, nil, "inspect", "--type", "container", "--format", "{{.State.Status}} {{.State.ExitCode}}", container)
	if err != nil {
		return ContainerState{}, fmt.Errorf("reading the state of %s: %w", container, err)
	}
	fields := strings.Fields(string(out))
	if len(fields) != 2 {
		return ContainerState{}, fmt.Errorf("reading the state of %s: docker inspect answered %q", container, strings.TrimSpace(string(out)))
	}
	code, err := strconv.Atoi(fields[1])
	if err != nil {
		return ContainerState{}, fmt.Errorf("reading the state of %s: exit code %q: %w", container, fields[1], err)
	}
	return ContainerState{Status: fields[0], ExitCode: code}, nil
}

// Wait blocks until the container stops and returns its exit code (`docker wait`): the wallet-backend build
// container.
func (r *Runner) Wait(ctx context.Context, container string) (int, error) {
	out, _, err := r.call(ctx, nil, "wait", container)
	if err != nil {
		return -1, fmt.Errorf("waiting for %s: %w", container, err)
	}
	code, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return -1, fmt.Errorf("waiting for %s: docker wait answered %q", container, strings.TrimSpace(string(out)))
	}
	return code, nil
}

// Start starts a stopped container again (`docker start`): the pre-fix ingester's one restart, the same container
// with its own log (PRE_FIX_RESTART_COUNT).
func (r *Runner) Start(ctx context.Context, container string) error {
	if _, _, err := r.call(ctx, nil, "start", container); err != nil {
		return fmt.Errorf("starting %s again: %w", container, err)
	}
	return nil
}

// Stop stops a running container, giving it grace before it is killed (`docker stop -t`). Its log stays readable
// until it is removed.
func (r *Runner) Stop(ctx context.Context, container string, grace time.Duration) error {
	secs := int(grace / time.Second)
	if _, _, err := r.call(ctx, nil, "stop", "-t", strconv.Itoa(secs), container); err != nil {
		return fmt.Errorf("stopping %s: %w", container, err)
	}
	return nil
}

// Remove removes the container and its anonymous volumes (`docker rm -f -v`), before the run's reaper does.
func (r *Runner) Remove(ctx context.Context, container string) error {
	if _, _, err := r.call(ctx, nil, "rm", "-f", "-v", container); err != nil {
		return fmt.Errorf("removing %s: %w", container, err)
	}
	return nil
}

// TimestampedLogs returns the container's whole log, standard output and standard error together, each line led by
// Docker's timestamp (`docker logs --timestamps`) and the lines in time order. Docker keeps one log across a `docker
// start`, so a restarted container's lines follow those of its first run: the first n lines of a later call are the n
// lines of an earlier one: one log file holds both runs.
func (r *Runner) TimestampedLogs(ctx context.Context, container string) ([]string, error) {
	out, errOut, err := r.call(ctx, nil, "logs", "--timestamps", container)
	if err != nil {
		return nil, fmt.Errorf("reading the log of %s: %w", container, err)
	}
	return mergeTimestamped(string(out), string(errOut)), nil
}

// mergeTimestamped merges the timestamped lines of two streams into time order; a line whose timestamp does not parse
// keeps its place after the line before it in its own stream.
func mergeTimestamped(streams ...string) []string {
	type line struct {
		at   time.Time
		text string
	}
	var all []line
	for _, s := range streams {
		var last time.Time
		sc := bufio.NewScanner(strings.NewReader(s))
		sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
		for sc.Scan() {
			text := sc.Text()
			stamp, _, _ := strings.Cut(text, " ")
			if at, err := time.Parse(time.RFC3339Nano, stamp); err == nil {
				last = at
			}
			all = append(all, line{at: last, text: text})
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].at.Before(all[j].at) })
	out := make([]string, len(all))
	for i, l := range all {
		out[i] = l.text
	}
	return out
}

// CopyInto extracts the tar stream tarball into the directory dir of the container (`docker cp - <container>:<dir>`;
// dir must exist there). The js-sdk runner copies the embedded script and one release's package files this way, so the
// reproduction does not depend on bind-mount sharing.
func (r *Runner) CopyInto(ctx context.Context, container, dir string, tarball []byte) error {
	if _, _, err := r.call(ctx, tarball, "cp", "-", container+":"+dir); err != nil {
		return fmt.Errorf("copying files into %s:%s: %w", container, dir, err)
	}
	return nil
}
