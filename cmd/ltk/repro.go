package main

// What the two `ltk repro` commands share: where the report is written, how it is printed, and how the verdict's
// reasons become the ErrorLine's message.

import (
	"strings"
)

// reproWorkingDir is the folder under which `ltk repro` writes repro/<target>.json: the working directory the program
// runs in. Nothing else is written there.
const reproWorkingDir = "."

// printReproReport writes the report with write, then prints the bytes written on stdout, so standard output is
// byte-identical to the saved file. When the write fails nothing is printed: the run ends with exit 1 and the
// ErrorLine naming the path.
func printReproReport(inv invocation, write func() ([]byte, error)) error {
	b, err := write()
	if err != nil {
		return err
	}
	_, err = inv.stdout.Write(b)
	return err
}

// mergeReasons is the reasons of first, then of more, in order, each once.
func mergeReasons(first, more []string) []string {
	var out []string
	for _, r := range append(append([]string(nil), first...), more...) {
		dup := false
		for _, have := range out {
			dup = dup || have == r
		}
		if !dup {
			out = append(out, r)
		}
	}
	return out
}

// joinReasons is the reasons of a verdict not passed, as the ErrorLine's message carries them: joined
// by "; ", or a plain statement when the runner gave none.
func joinReasons(reasons []string) string {
	if len(reasons) == 0 {
		return "the report's passed is false"
	}
	return strings.Join(reasons, "; ")
}
