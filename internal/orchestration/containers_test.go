package orchestration

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"
	"time"
)

// The wallet-backend binaries' volume is created with both labels and the run's name, and a
// container mounts it read-only with `-v <volume>:<target>:ro`.
func TestCreateVolumeIsLabelledAndMounted(t *testing.T) {
	f := newFake(t)
	r := f.runner("a1b2c3d4")
	ctx := context.Background()
	vol, err := r.CreateVolume(ctx, "wb-bin")
	if err != nil {
		t.Fatal(err)
	}
	if vol != "ltk-a1b2c3d4-wb-bin" {
		t.Fatalf("volume name %q", vol)
	}
	if _, err := r.Run(ctx, ContainerSpec{Role: "wb-2a0a84ea", Image: GoBuildImage, Network: "ltk-a1b2c3d4-net",
		Mounts: []Mount{{Volume: vol, Target: "/app", ReadOnly: true}, {Volume: vol, Target: "/out"}}}); err != nil {
		t.Fatal(err)
	}
	calls := f.calls()
	if len(calls) != 2 {
		t.Fatalf("calls %q", f.argLines())
	}
	create := calls[0].Args
	if !slices.Equal(create[:2], []string{"volume", "create"}) || create[len(create)-1] != vol {
		t.Fatalf("volume create call %q", create)
	}
	labels := flagValues(create, "--label")
	if !slices.Equal(labels, []string{"ltk.managed=true", "ltk.run=a1b2c3d4"}) {
		t.Fatalf("volume labels %q", labels)
	}
	mounts := flagValues(calls[1].Args, "-v")
	if !slices.Equal(mounts, []string{"ltk-a1b2c3d4-wb-bin:/app:ro", "ltk-a1b2c3d4-wb-bin:/out"}) {
		t.Fatalf("mounts %q", mounts)
	}
	if i, img := slices.Index(calls[1].Args, "-v"), slices.Index(calls[1].Args, GoBuildImage); i < 0 || i > img {
		t.Fatalf("-v must come before the image: %q", calls[1].Args)
	}
}

// The state of a container is its status and its last exit code.
func TestStateReadsStatusAndExitCode(t *testing.T) {
	f := newFake(t, fakeRule{Prefix: []string{"inspect"}, Stdout: "exited 2\n"})
	st, err := f.runner("a1b2c3d4").State(context.Background(), "ltk-a1b2c3d4-wb-2a0a84ea")
	if err != nil {
		t.Fatal(err)
	}
	if st.Status != "exited" || st.ExitCode != 2 || !st.Exited() {
		t.Fatalf("state %+v", st)
	}
	want := "inspect --type container --format {{.State.Status}} {{.State.ExitCode}} ltk-a1b2c3d4-wb-2a0a84ea"
	if got := f.argLines(); len(got) != 1 || got[0] != want {
		t.Fatalf("calls %q; want %q", got, want)
	}
	if (ContainerState{Status: "running"}).Exited() {
		t.Fatal("a running container reported as exited")
	}
}

func TestStateRejectsAnAnswerItCannotRead(t *testing.T) {
	for _, answer := range []string{"running\n", "exited two\n", ""} {
		f := newFake(t, fakeRule{Prefix: []string{"inspect"}, Stdout: answer})
		if _, err := f.runner("a1b2c3d4").State(context.Background(), "c"); err == nil {
			t.Errorf("State accepted %q", answer)
		}
	}
}

// The build container's exit code comes from `docker wait`.
func TestWaitReturnsTheExitCode(t *testing.T) {
	f := newFake(t, fakeRule{Prefix: []string{"wait"}, Stdout: "17\n"})
	code, err := f.runner("a1b2c3d4").Wait(context.Background(), "ltk-a1b2c3d4-wb-build")
	if err != nil || code != 17 {
		t.Fatalf("Wait = %d, %v", code, err)
	}
	f = newFake(t, fakeRule{Prefix: []string{"wait"}, Stderr: "Error: No such container: c", Exit: 1})
	if _, err := f.runner("a1b2c3d4").Wait(context.Background(), "c"); err == nil || !strings.Contains(err.Error(), "No such container") {
		t.Fatalf("Wait on a failing docker: %v", err)
	}
}

// The restart is `docker start` of the same container; stop and remove are docker's own verbs.
func TestStartStopRemoveCalls(t *testing.T) {
	f := newFake(t)
	r := f.runner("a1b2c3d4")
	ctx := context.Background()
	if err := r.Start(ctx, "c1"); err != nil {
		t.Fatal(err)
	}
	if err := r.Stop(ctx, "c1", 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := r.Remove(ctx, "c1"); err != nil {
		t.Fatal(err)
	}
	want := []string{"start c1", "stop -t 5 c1", "rm -f -v c1"}
	if got := f.argLines(); !slices.Equal(got, want) {
		t.Fatalf("calls %q; want %q", got, want)
	}
	f = newFake(t, fakeRule{Prefix: []string{"start"}, Stderr: "Error response from daemon: boom", Exit: 1})
	if err := f.runner("a1b2c3d4").Start(ctx, "c1"); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("Start on a failing docker: %v", err)
	}
}

// The log of both runs is read whole, standard output and standard error merged in time order; Docker's
// timestamps drop trailing zeros, so they are compared as times, not as text.
func TestTimestampedLogsMergesStreamsInTimeOrder(t *testing.T) {
	stdout := "2026-11-14T22:53:10.5Z out-b\n2026-11-14T22:53:12Z out-d\n"
	stderr := "2026-11-14T22:53:10.123456789Z err-a\n2026-11-14T22:53:11.9Z err-c\ncontinuation\n"
	f := newFake(t, fakeRule{Prefix: []string{"logs"}, Stdout: stdout, Stderr: stderr})
	lines, err := f.runner("a1b2c3d4").TimestampedLogs(context.Background(), "c1")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, l := range lines {
		got = append(got, l[strings.LastIndex(l, " ")+1:])
	}
	want := []string{"err-a", "out-b", "err-c", "continuation", "out-d"}
	if !slices.Equal(got, want) {
		t.Fatalf("order %q; want %q", got, want)
	}
	if args := f.argLines(); len(args) != 1 || args[0] != "logs --timestamps c1" {
		t.Fatalf("calls %q", args)
	}
}

// Files go into the container as a tar stream on `docker cp -`, never through a bind mount.
func TestCopyIntoSendsTheTarOnStandardInput(t *testing.T) {
	f := newFake(t)
	tarball := []byte("not really a tar, the fake does not read it")
	if err := f.runner("a1b2c3d4").CopyInto(context.Background(), "ltk-a1b2c3d4-sdk-17-2-0", "/tmp", tarball); err != nil {
		t.Fatal(err)
	}
	calls := f.calls()
	if len(calls) != 1 || strings.Join(calls[0].Args, " ") != "cp - ltk-a1b2c3d4-sdk-17-2-0:/tmp" {
		t.Fatalf("calls %q", f.argLines())
	}
	if !bytes.Equal(calls[0].Stdin, tarball) {
		t.Fatalf("stdin %q", calls[0].Stdin)
	}
}
