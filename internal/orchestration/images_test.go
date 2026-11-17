package orchestration

import (
	"context"
	"errors"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/verticalhorizonai/ltk/internal/contract"
	"github.com/verticalhorizonai/ltk/internal/model"
)

// Every image reference in images.go is repository@sha256:<64 hex>, with no tag, and the five pinned images are the
// two quickstarts, the Go build image, TimescaleDB and Node.
func TestImagesArePinnedByDigest(t *testing.T) {
	src, err := os.ReadFile("images.go")
	if err != nil {
		t.Fatal(err)
	}
	literal := regexp.MustCompile(`"([^"\n]*(?:@sha256:|:latest)[^"\n]*)"`)
	pinned := regexp.MustCompile(`^[a-z0-9._/-]+@sha256:[0-9a-f]{64}$`)
	found := literal.FindAllStringSubmatch(string(src), -1)
	if len(found) != 5 {
		t.Fatalf("%d image references in images.go; want 5", len(found))
	}
	for _, m := range found {
		if !pinned.MatchString(m[1]) {
			t.Errorf("image reference %q is not repository@sha256:<digest>", m[1])
		}
	}
	want := map[string]string{
		QuickstartCurrent:    "stellar/quickstart@sha256:a61ce821add98adb4f3e3d6e757b2615dd169a2b39c7e30cd47cfa95074f97d9",
		QuickstartProtocol28: "stellar/quickstart@sha256:057b31d5c23d90d58c4637c2888163f1c78afd6c08941339121f4feb9d78b087",
		GoBuildImage:         "golang@sha256:298734aec230b5f3e8cee450ce6d7eccc39f1797ba548ee90d57e9803030c6c3",
		TimescaleDBImage:     "timescale/timescaledb@sha256:909e2c7577c074517c8936b6d8799294b33f25fb5bad978a2199250665f1ce1a",
		NodeImage:            "node@sha256:e9f565d32859f0cbfb52125eafe8be39390f24dac302dc3cdf566e8ba3ab21ee",
	}
	for got, w := range want {
		if got != w {
			t.Errorf("image %q; want %q", got, w)
		}
	}
	if CurrentProtocol != 30 || FallbackProtocol != 28 {
		t.Fatalf("CurrentProtocol %d, FallbackProtocol %d; want 30 (the current quickstart's default) and 28", CurrentProtocol, FallbackProtocol)
	}
	for _, set := range [][]string{GenerateImages, CheckImages, WalletBackendImages, JsSdkImages} {
		for _, ref := range set {
			if _, ok := want[ref]; !ok {
				t.Errorf("an image set names %q, not a pinned image", ref)
			}
		}
	}
}

// A present image is inspected and not pulled.
func TestEnsureImagesPresent(t *testing.T) {
	f := newFake(t, fakeRule{Prefix: []string{"image", "inspect"}, Stdout: "sha256:abc\n"})
	if err := f.runner("0f0f0f0f").EnsureImages(context.Background(), CheckImages); err != nil {
		t.Fatal(err)
	}
	want := []string{"image inspect --format {{.Id}} " + QuickstartCurrent}
	if got := f.argLines(); !slices.Equal(got, want) {
		t.Fatalf("calls %q; want %q", got, want)
	}
}

// An absent image is pulled by its digest.
func TestEnsureImagesPullsAnAbsentImage(t *testing.T) {
	f := newFake(t, fakeRule{Prefix: []string{"image", "inspect", "--format", "{{.Id}}", NodeImage}, Stderr: "Error: No such image\n", Exit: 1})
	if err := f.runner("0f0f0f0f").EnsureImages(context.Background(), JsSdkImages); err != nil {
		t.Fatal(err)
	}
	want := []string{"image inspect --format {{.Id}} " + NodeImage, "pull " + NodeImage}
	if got := f.argLines(); !slices.Equal(got, want) {
		t.Fatalf("calls %q; want %q", got, want)
	}
}

// A pull that fails is an environment error (exit 3) naming the image, and the
// images after it are not touched.
func TestEnsureImagesPullFailureIsEnvironmentError(t *testing.T) {
	f := newFake(t,
		fakeRule{Prefix: []string{"image", "inspect"}, Stderr: "Error: No such image\n", Exit: 1},
		fakeRule{Prefix: []string{"pull"}, Stderr: "Error response from daemon: Get \"https://registry-1.docker.io/v2/\": dial tcp: lookup registry-1.docker.io: no such host\n", Exit: 1})
	err := f.runner("0f0f0f0f").EnsureImages(context.Background(), WalletBackendImages)
	var coded *model.CodedError
	if !errors.As(err, &coded) || coded.Code != contract.ErrorCodeEnvironmentMissing {
		t.Fatalf("EnsureImages: %v; want environment_missing", err)
	}
	if !strings.HasPrefix(coded.Message, "digest-pinned image cannot be pulled: "+QuickstartProtocol28) {
		t.Fatalf("message %q does not name the image", coded.Message)
	}
	if n := len(f.calls()); n != 2 {
		t.Fatalf("%d calls after the failed pull; want 2: %q", n, f.argLines())
	}
}
