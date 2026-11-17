package orchestration

import (
	"context"
	"slices"
	"strings"
	"testing"
)

// The reaper removes by this run's label, containers first with `rm -f -v`, then networks, then volumes.
func TestReapOrderAndVolumes(t *testing.T) {
	f := newFake(t,
		fakeRule{Prefix: []string{"ps"}, Stdout: "c1\nc2\n"},
		fakeRule{Prefix: []string{"network", "ls"}, Stdout: "n1\n"},
		fakeRule{Prefix: []string{"volume", "ls"}, Stdout: "v1\n"})
	if err := f.runner("feedf00d").Reap(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"ps -a -q --no-trunc --filter label=ltk.run=feedf00d",
		"rm -f -v c1 c2",
		"network ls -q --no-trunc --filter label=ltk.run=feedf00d",
		"network rm n1",
		"volume ls -q --filter label=ltk.run=feedf00d",
		"volume rm v1",
	}
	if got := f.argLines(); !slices.Equal(got, want) {
		t.Fatalf("calls:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// Nothing labelled, nothing removed.
func TestReapWithNothingToRemove(t *testing.T) {
	f := newFake(t)
	if err := f.runner("feedf00d").Reap(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, c := range f.calls() {
		if c.Args[0] == "rm" || (len(c.Args) > 1 && c.Args[1] == "rm") {
			t.Fatalf("a removal with nothing listed: %q", c.Args)
		}
	}
}

// A removal that fails is reported, naming the resource, and the later kinds are still removed.
func TestReapReportsFailuresAndContinues(t *testing.T) {
	f := newFake(t,
		fakeRule{Prefix: []string{"ps"}, Stdout: "c1\n"},
		fakeRule{Prefix: []string{"rm"}, Stderr: "Error response from daemon: removal of container c1 is already in progress\n", Exit: 1},
		fakeRule{Prefix: []string{"network", "ls"}, Stdout: "n1\n"})
	err := f.runner("feedf00d").Reap(context.Background())
	if err == nil || !strings.Contains(err.Error(), "c1") || !strings.Contains(err.Error(), "removal of container c1") {
		t.Fatalf("Reap: %v", err)
	}
	if !slices.Contains(f.argLines(), "network rm n1") {
		t.Fatalf("the networks were not removed after the container failure: %q", f.argLines())
	}
}

// The reaper runs even when the command's context is already cancelled (an interrupt).
func TestReapRunsOnACancelledContext(t *testing.T) {
	f := newFake(t, fakeRule{Prefix: []string{"ps"}, Stdout: "c1\n"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := f.runner("feedf00d").Reap(ctx); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(f.argLines(), "rm -f -v c1") {
		t.Fatalf("calls %q", f.argLines())
	}
}

// Snapshot lists every container (running or stopped), network and volume, not only labelled ones.
func TestSnapshotListsEverything(t *testing.T) {
	f := newFake(t,
		fakeRule{Prefix: []string{"ps"}, Stdout: "id1\tnotion-db\nid2\tltk-feedf00d-core\n"},
		fakeRule{Prefix: []string{"network", "ls"}, Stdout: "nid1\tbridge\n"},
		fakeRule{Prefix: []string{"volume", "ls"}, Stdout: "3f9a\t3f9a\n"})
	s, err := f.runner("feedf00d").Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if s.Containers["id1"] != "container notion-db" || s.Containers["id2"] != "container ltk-feedf00d-core" ||
		s.Networks["nid1"] != "network bridge" || s.Volumes["3f9a"] != "volume 3f9a" {
		t.Fatalf("snapshot %+v", s)
	}
	for _, line := range f.argLines() {
		if strings.Contains(line, "--filter") {
			t.Fatalf("Snapshot filtered a listing: %q", line)
		}
	}
	want := []string{
		"ps -a --no-trunc --format {{.ID}}\t{{.Names}}",
		"network ls --no-trunc --format {{.ID}}\t{{.Name}}",
		"volume ls --format {{.Name}}\t{{.Name}}",
	}
	if got := f.argLines(); !slices.Equal(got, want) {
		t.Fatalf("calls %q; want %q", got, want)
	}
}

// Leftovers is what after has and before had not, an anonymous volume included; what both have
// (another project's resources) is not counted.
func TestLeftovers(t *testing.T) {
	before := Snapshot{
		Containers: map[string]string{"id1": "container notion-db"},
		Networks:   map[string]string{"nid1": "network bridge"},
		Volumes:    map[string]string{"keep": "volume keep"},
	}
	if got := Leftovers(before, before); len(got) != 0 {
		t.Fatalf("Leftovers(before, before) = %q", got)
	}
	after := Snapshot{
		Containers: map[string]string{"id1": "container notion-db", "id9": "container ltk-feedf00d-db"},
		Networks:   map[string]string{"nid1": "network bridge", "nid9": "network ltk-feedf00d-net"},
		Volumes:    map[string]string{"keep": "volume keep", "0a1b": "volume 0a1b"},
	}
	want := []string{"container ltk-feedf00d-db", "network ltk-feedf00d-net", "volume 0a1b"}
	if got := Leftovers(before, after); !slices.Equal(got, want) {
		t.Fatalf("Leftovers = %q; want %q", got, want)
	}
	// A resource gone since before is not a leftover.
	if got := Leftovers(after, before); len(got) != 0 {
		t.Fatalf("Leftovers(after, before) = %q", got)
	}
}
