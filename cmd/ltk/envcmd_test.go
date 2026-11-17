package main

// The Container-dependent command verification test. The commands that start a container (`ltk generate`, `ltk check`
// for its round trip, `ltk repro`) run where Docker with Linux containers is available; where it is not, they exit 3
// and say what is missing.
//
// Each container-dependent command is run, as built, under each of the environment check's three conditions (no docker
// CLI, no reachable daemon, a daemon without Linux containers) with the fake docker of testdata/fakedocker. This file
// has no build tag: its rows run in the unit tier on every system with a go tool and in the integration tier too. The
// real `ltk check` round trip needs Docker and is tested in the integration tier; these rows need none (the fake stands
// for the engine), so they are here, in both tiers (as check_test.go's are). The rows that see each command proceed
// with Docker available are envcmd_integration_test.go's.

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/verticalhorizonai/ltk/internal/catalogue"
	"github.com/verticalhorizonai/ltk/internal/contract"
	"github.com/verticalhorizonai/ltk/internal/model"
	"github.com/verticalhorizonai/ltk/internal/orchestration"
)

// containerCommand is one container-dependent invocation.
type containerCommand struct {
	// name is the command's name in its ErrorLine: "ltk <name>: …".
	name string
	// label names the row.
	label string
	// args builds the arguments given a fresh temporary folder.
	args func(tmp string) []string
	// inRoot runs the command from the module root (the folder argument `corpus/`); otherwise from the temporary folder.
	inRoot bool
	// image is the first image of the command's set, the one named when it cannot be pulled.
	image string
}

// containerCommands are the five container-dependent invocations.
var containerCommands = []containerCommand{
	{"generate", "generate --all --out <folder>", func(tmp string) []string {
		return []string{"generate", "--all", "--out", filepath.Join(tmp, "out")}
	}, false, orchestration.QuickstartCurrent},
	{"generate", "generate --shape <entry id> --out <folder>", func(tmp string) []string {
		return []string{"generate", "--shape", model.SeedMuxedAccount, "--out", filepath.Join(tmp, "out")}
	}, false, orchestration.QuickstartCurrent},
	{"check", "check corpus/", func(string) []string { return []string{"check", corpusArg} }, true, orchestration.QuickstartCurrent},
	{"repro " + string(contract.ReproTargetWalletBackend729), "repro wallet-backend-729", func(string) []string {
		return []string{"repro", string(contract.ReproTargetWalletBackend729)}
	}, false, orchestration.WalletBackendImages[0]},
	{"repro " + string(contract.ReproTargetJsSdk1766), "repro js-sdk-1766", func(string) []string {
		return []string{"repro", string(contract.ReproTargetJsSdk1766)}
	}, false, orchestration.JsSdkImages[0]},
}

// TestProcessContainerCommandsEnvironmentMissing (the fail-fast rows): for each command and each condition, exit 3,
// nothing on standard output, one environment_missing ErrorLine whose message starts with the command and holds the
// condition's substring ("docker CLI", "daemon", "Linux containers") and the check's full wording, and the fake called
// with nothing but `version` (not at all when it is not on PATH). Nothing is created: no --out folder, no repro/ under
// the working directory, no file under corpus/ changed. A fourth row per command, an image of its set absent and
// unpullable, exits 3 naming the image (a digest-pinned image that cannot be pulled gives the same exit code and error
// code as a missing engine), having called only `version`, `image inspect` and `pull`.
func TestProcessContainerCommandsEnvironmentMissing(t *testing.T) {
	const missing = "Docker with Linux containers is not available: "
	conditions := []struct {
		name, modes, substring, message string
		noDocker                        bool
		calls                           []string
	}{
		{"1 docker CLI not on PATH", "", "docker CLI", missing + "docker CLI not found on PATH", true, nil},
		{"2 daemon not reachable", "no-daemon", "daemon", missing + "Docker daemon not reachable: Cannot connect to the Docker daemon", false, []string{"version"}},
		{"3 not Linux containers", "windows", "Linux containers", missing + `Docker is not running Linux containers (its daemon runs "windows" containers)`, false, []string{"version"}},
		{"image cannot be pulled", "no-image", "", "digest-pinned image cannot be pulled: ", false, nil},
	}
	root := acceptRoot(t)
	corpus := filepath.Join(root, "corpus")
	for _, cmd := range containerCommands {
		for _, cond := range conditions {
			t.Run(cmd.label+" "+cond.name, func(t *testing.T) {
				var f *fakeEngine
				env := noDockerEnv(t)
				switch {
				case cond.noDocker && fakeDockerDir == "":
					// condition 1 needs no fake: it runs on every system
				case cond.noDocker:
					f = newFakeEngine(t, cond.modes, corpus)
					env = append(withoutPath(f.env), "PATH="+t.TempDir()) // the fake is not on PATH; its log stays empty
				default:
					f = newFakeEngine(t, cond.modes, corpus)
					env = f.env
				}
				tmp := t.TempDir()
				dir := tmp
				if cmd.inRoot {
					dir = root
				}
				before := treeOf(t, corpus)
				code, stdout, stderr := runLTKIn(t, dir, env, cmd.args(tmp)...)
				if code != int(contract.EXIT_CODES.EnvironmentMissing) || len(stdout) != 0 {
					t.Fatalf("exit %d stdout %q stderr %q; want 3 and nothing on stdout", code, stdout, stderr)
				}
				line := assertErrorLine(t, stderr, contract.ErrorCodeEnvironmentMissing)
				wantMessage := cond.message
				if cond.modes == "no-image" {
					wantMessage += cmd.image
				}
				if !strings.HasPrefix(line.Message, "ltk "+cmd.name+": ") || !strings.Contains(line.Message, cond.substring) ||
					!strings.Contains(line.Message, wantMessage) {
					t.Errorf("message %q\nwant \"ltk %s: …%s…\"", line.Message, cmd.name, wantMessage)
				}
				if f != nil {
					got := f.subcommands()
					if cond.modes == "no-image" {
						for _, c := range got {
							if !slices.Contains([]string{"version", "image inspect", "pull"}, c) {
								t.Errorf("the fake was called with %v: only version, image inspect and pull before the image is had", got)
								break
							}
						}
					} else if !slices.Equal(got, cond.calls) {
						t.Errorf("the fake was called with %v, want %v: nothing but version", got, cond.calls)
					}
				}
				if exists(filepath.Join(tmp, "out")) || exists(filepath.Join(tmp, catalogue.ReproFolder)) || exists(filepath.Join(root, "out")) {
					t.Errorf("a folder was created")
				}
				assertTreeUnchanged(t, "corpus/", before, treeOf(t, corpus))
			})
		}
	}
}

// withoutPath is env without its PATH entry.
func withoutPath(env []string) []string {
	var out []string
	for _, kv := range env {
		if !strings.HasPrefix(kv, "PATH=") {
			out = append(out, kv)
		}
	}
	return out
}
