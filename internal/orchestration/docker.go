package orchestration

// The Docker CLI runner: the one adapter through which the tool reaches Docker. It runs the `docker` executable with
// os/exec, and every call that creates a resource adds the two labels, ltk.managed=true and ltk.run=<runID>, on one
// code path, so no caller can leave them out.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"unicode/utf8"

	"github.com/verticalhorizonai/ltk/internal/model"
)

const (
	// LabelManaged is the label every container, network and volume the tool creates carries.
	LabelManaged = "ltk.managed"
	// LabelRun is the label naming the run that created the resource: ltk.run=<runID>.
	LabelRun = "ltk.run"
	// namePrefix starts every resource name: ltk-<runID>-<role>.
	namePrefix = "ltk"
)

// NewRunID returns a fresh run id: 8 lowercase hex characters from crypto/rand, made once per invocation.
func NewRunID() (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("making the run id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// Runner is the Docker CLI runner of one run: it calls the `docker` executable found on PATH, and labels and names
// every resource it creates with its run id.
type Runner struct {
	bin   string
	runID string
}

// NewRunner finds the `docker` executable with exec.LookPath and returns the runner of the run runID. A missing CLI is
// an environment error (exit 3): "docker CLI not found on PATH".
func NewRunner(runID string) (*Runner, error) {
	if len(runID) != 8 {
		return nil, fmt.Errorf("run id %q is not 8 hex characters", runID)
	}
	if _, err := hex.DecodeString(runID); err != nil {
		return nil, fmt.Errorf("run id %q is not 8 hex characters", runID)
	}
	bin, err := exec.LookPath("docker")
	if err != nil {
		return nil, model.Environment("docker CLI not found on PATH")
	}
	return &Runner{bin: bin, runID: runID}, nil
}

// Name returns the resource name of role in this run: ltk-<runID>-<role>.
func (r *Runner) Name(role string) string { return namePrefix + "-" + r.runID + "-" + role }

// runFilter is the `--filter` value that selects this run's resources.
func (r *Runner) runFilter() string { return "label=" + LabelRun + "=" + r.runID }

// labelArgs are the label flags every create call carries.
func (r *Runner) labelArgs() []string {
	return []string{"--label", LabelManaged + "=true", "--label", LabelRun + "=" + r.runID}
}

// CommandError is a `docker` call that failed: its arguments, its exit code (-1 when it did not run or was killed)
// and its standard error. Error() names only the subcommand and the resource; the whole call stays in Args and the
// whole standard error in Stderr.
type CommandError struct {
	// Args are the arguments of the call, without the executable.
	Args []string
	// ExitCode is the exit code of `docker`; -1 when it did not start or ended by a signal or a cancelled context.
	ExitCode int
	// Stderr is what `docker` wrote to standard error, trimmed.
	Stderr string
	// Err is the underlying error of os/exec.
	Err error
}

// maxDockerMessage bounds docker's message in CommandError.Error(), as maxToolMessage bounds the Rust XDR tool's to
// the first line of its standard error, at most 300 bytes: the message ends up in the one ErrorLine.
const maxDockerMessage = 300

// Error names the docker subcommand and the container or other resource it was about, then the first line of
// docker's standard error, at most maxDockerMessage bytes; when docker wrote nothing, the os/exec error on one line.
// It never repeats the call's other arguments: a `docker run` carries the container's environment and command (a
// whole build script for wallet-backend), which do not belong in an ErrorLine.
func (e *CommandError) Error() string {
	sub, resource := describeCall(e.Args)
	msg := "docker " + sub
	if resource != "" {
		msg += " " + resource
	}
	if line := firstLine(e.Stderr); line != "" {
		return msg + ": " + capLine(line, maxDockerMessage)
	}
	if e.Err == nil {
		return msg + ": failed"
	}
	return msg + ": " + capLine(strings.Join(strings.Fields(strings.ReplaceAll(e.Err.Error(), "\n", "; ")), " "), maxDockerMessage)
}

// Unwrap returns the os/exec error, so a cancelled context is seen through the error chain.
func (e *CommandError) Unwrap() error { return e.Err }

// managementCommands are the docker commands whose subcommand is their second word (`docker network create`).
var managementCommands = map[string]bool{"network": true, "volume": true, "image": true, "container": true}

// valueFlags are the flags of the runner's calls that take the next argument as their value.
var valueFlags = map[string]bool{"--filter": true, "--format": true, "--type": true, "--tail": true, "-t": true, "--label": true}

// describeCall returns the subcommand of a docker call ("run", "network create") and the resource it was about: the
// container's name for `run`, the container for `exec`, `port` and `cp`, nothing for `version` and the listings, and
// otherwise its operands (the arguments that are neither flags nor flag values), space-separated.
func describeCall(args []string) (sub, resource string) {
	if len(args) == 0 {
		return "", ""
	}
	sub, rest := args[0], args[1:]
	if managementCommands[args[0]] && len(args) > 1 {
		sub, rest = args[0]+" "+args[1], args[2:]
	}
	switch sub {
	case "run", "create", "container run", "container create":
		for i := 0; i+1 < len(rest); i++ {
			if rest[i] == "--name" {
				return sub, rest[i+1]
			}
		}
		return sub, ""
	case "exec", "port":
		for _, a := range rest {
			if !strings.HasPrefix(a, "-") {
				return sub, a
			}
		}
		return sub, ""
	case "cp":
		for _, a := range rest {
			if container, _, ok := strings.Cut(a, ":"); ok && a != "-" {
				return sub, container
			}
		}
		return sub, ""
	case "version", "ps", "network ls", "volume ls", "image ls":
		return sub, ""
	}
	var operands []string
	for i := 0; i < len(rest); i++ {
		switch {
		case valueFlags[rest[i]]:
			i++
		case strings.HasPrefix(rest[i], "-"):
		default:
			operands = append(operands, rest[i])
		}
	}
	return sub, strings.Join(operands, " ")
}

// firstLine is the first non-empty line of s, trimmed.
func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return ""
}

// capLine is s cut to at most max bytes, at a UTF-8 boundary, with "…" after a cut.
func capLine(s string, max int) string {
	if len(s) <= max {
		return s
	}
	n := max
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}

// call runs `docker args...` with stdin (nil for none) and returns its standard output and standard error. A
// non-zero exit is a *CommandError.
func (r *Runner) call(ctx context.Context, stdin []byte, args ...string) (stdout, stderr []byte, err error) {
	cmd := exec.CommandContext(ctx, r.bin, args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if runErr := cmd.Run(); runErr != nil {
		code := -1
		var exit *exec.ExitError
		if errors.As(runErr, &exit) {
			code = exit.ExitCode()
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			runErr = errors.Join(runErr, ctxErr)
		}
		return out.Bytes(), errOut.Bytes(), &CommandError{Args: args, ExitCode: code, Stderr: strings.TrimSpace(errOut.String()), Err: runErr}
	}
	return out.Bytes(), errOut.Bytes(), nil
}

// create runs a create call: the subcommand words, then the two labels, then the rest. Every container, network and
// volume the tool makes is made through here, so no call path can omit the labels.
func (r *Runner) create(ctx context.Context, sub []string, rest ...string) ([]byte, error) {
	args := append(append(append([]string{}, sub...), r.labelArgs()...), rest...)
	out, _, err := r.call(ctx, nil, args...)
	return out, err
}

// ServerOS returns the operating system of the Docker daemon's containers, `docker version --format {{.Server.Os}}`
// trimmed: "linux" when Docker runs Linux containers. The Container environment check (cmd/ltk/envcheck.go) calls it
// under its own deadline; a daemon that does not answer is a *CommandError carrying docker's standard error.
func (r *Runner) ServerOS(ctx context.Context) (string, error) {
	out, _, err := r.call(ctx, nil, "version", "--format", "{{.Server.Os}}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// CreateNetwork creates the run's labelled bridge network ltk-<runID>-<role> and returns its name: every local network
// runs on a Docker network of its own.
func (r *Runner) CreateNetwork(ctx context.Context, role string) (string, error) {
	name := r.Name(role)
	if _, err := r.create(ctx, []string{"network", "create"}, name); err != nil {
		return "", fmt.Errorf("creating the network %s: %w", name, err)
	}
	return name, nil
}

// ContainerSpec is one container to run detached by Run.
type ContainerSpec struct {
	// Role makes the container's name, ltk-<runID>-<role>.
	Role string
	// Image is the digest-pinned image reference, one of images.go's constants.
	Image string
	// Network is the network to attach to: a network of this run, or "none".
	Network string
	// Publish lists the container ports to publish, each bound to an ephemeral port of 127.0.0.1 only
	// (`-p 127.0.0.1::<port>`): every published port is bound to 127.0.0.1.
	Publish []int
	// Env holds NAME=value pairs for the container's environment.
	Env []string
	// Mounts are the run's labelled volumes to mount (`-v <volume>:<target>[:ro]`): the wallet-backend binaries'
	// volume. Only volumes of this run are mounted; nothing of the host is.
	Mounts []Mount
	// Entrypoint, when set, replaces the image's entrypoint.
	Entrypoint string
	// Args are the arguments after the image: the container's command.
	Args []string
}

// Run starts the container of spec detached, labelled and named, and returns its name.
func (r *Runner) Run(ctx context.Context, spec ContainerSpec) (string, error) {
	name := r.Name(spec.Role)
	rest := []string{"--name", name}
	if spec.Network != "" {
		rest = append(rest, "--network", spec.Network)
	}
	for _, p := range spec.Publish {
		rest = append(rest, "-p", fmt.Sprintf("127.0.0.1::%d", p))
	}
	for _, e := range spec.Env {
		rest = append(rest, "-e", e)
	}
	for _, m := range spec.Mounts {
		rest = append(rest, "-v", m.arg())
	}
	if spec.Entrypoint != "" {
		rest = append(rest, "--entrypoint", spec.Entrypoint)
	}
	rest = append(append(rest, spec.Image), spec.Args...)
	if _, err := r.create(ctx, []string{"run", "-d"}, rest...); err != nil {
		return "", fmt.Errorf("starting the container %s: %w", name, err)
	}
	return name, nil
}

// Port returns the host address `docker port` reports for the container's published port, as host:port. A port bound
// to anything but 127.0.0.1 is an error.
func (r *Runner) Port(ctx context.Context, container string, port int) (string, error) {
	out, _, err := r.call(ctx, nil, "port", container, fmt.Sprintf("%d/tcp", port))
	if err != nil {
		return "", fmt.Errorf("reading the published port %d of %s: %w", port, container, err)
	}
	lines := strings.Fields(strings.TrimSpace(string(out)))
	if len(lines) != 1 {
		return "", fmt.Errorf("the port %d of %s is published as %q; want exactly one binding on 127.0.0.1", port, container, strings.TrimSpace(string(out)))
	}
	if !strings.HasPrefix(lines[0], "127.0.0.1:") {
		return "", fmt.Errorf("the port %d of %s is bound to %s, not to 127.0.0.1", port, container, lines[0])
	}
	return lines[0], nil
}

// Exec runs args in the running container with stdin on its standard input (`docker exec -i`) and returns its standard
// output and standard error; a non-zero exit is a *CommandError.
func (r *Runner) Exec(ctx context.Context, container string, stdin []byte, args ...string) (stdout, stderr []byte, err error) {
	if stdin == nil {
		stdin = []byte{}
	}
	return r.call(ctx, stdin, append([]string{"exec", "-i", container}, args...)...)
}

// Logs returns the last tail lines of the container's log, standard output and standard error together (a
// readiness error carries them).
func (r *Runner) Logs(ctx context.Context, container string, tail int) (string, error) {
	out, errOut, err := r.call(ctx, nil, "logs", "--tail", fmt.Sprint(tail), container)
	if err != nil {
		return "", err
	}
	return string(out) + string(errOut), nil
}

// state returns the container's state, `docker inspect --format {{.State.Status}}` ("running", "exited", ...).
func (r *Runner) state(ctx context.Context, container string) (string, error) {
	out, _, err := r.call(ctx, nil, "inspect", "--type", "container", "--format", "{{.State.Status}}", container)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
