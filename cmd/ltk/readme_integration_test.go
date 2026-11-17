//go:build integration

package main

// The README consistency test's integration row: `ltk check corpus/`, run from the module root with the Docker of this
// machine, exits as the README states, writes nothing to standard error, and prints the README's output byte for byte.
// Its round trip runs the Rust XDR tool in the pinned quickstart container, so it is in the integration tier, run
// natively on a machine with Docker.

import "testing"

// TestIntegrationReadmeCheckCorpus: the README's run of `ltk check corpus/` is the
// program's output on the committed corpus.
func TestIntegrationReadmeCheckCorpus(t *testing.T) {
	r := readmeRunOf(t, readmeRunsOf(t, readReadme(t)), "ltk check corpus/")
	code, stdout, stderr := runLTKIn(t, readmeModuleRoot, nil, r.args()...)
	if code != r.exit || len(stderr) != 0 {
		t.Errorf("ltk check corpus/: exit %d stderr %q; the README (line %d) states exit %d and nothing on stderr", code, stderr, r.line, r.exit)
	}
	if string(stdout) != r.stdout {
		t.Errorf("ltk check corpus/: standard output differs from the README's (line %d)\n--- program\n%s--- README\n%s", r.line, stdout, r.stdout)
	}
}
