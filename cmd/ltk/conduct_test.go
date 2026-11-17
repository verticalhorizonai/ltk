package main

// No file says how long the work took or what it cost: nothing in the repository or its documentation mentions how long
// the work of making it took or what it cost.
//
// The scan reads every file git lists under the module root, tracked or untracked and not ignored, outside the build's
// own planning files (isPackageOwn), which the exported repository does not carry, and skips binary files (a NUL byte).
// Outside a git work tree it skips, saying why. It does not read this file, which holds the patterns and their samples.
// It flags, case aside:
//
//   - a work verb with a span of time: "built", "written", "wrote", "developed", "made", "implemented", "coded",
//     "finished", "completed", "delivered", "spent", "spending", within 40 characters before "in", "within", "over",
//     "under", "for", "about", "around", "roughly", "nearly" or "almost", a number (a numeral or a number word), and a
//     unit: minutes, hours, days, weeks, months, evenings, afternoons, nights, weekends or sprints ("built in two
//     days");
//   - "spent" or "spending" directly followed by a number and one of those units ("spent 12 hours");
//   - "took" with a number and a unit of hours, days, weeks, months, weekends, evenings, afternoons or nights ("took
//     three days"); minutes are left out, because a run of the program takes minutes;
//   - a number of person-, man-, engineer-, developer- or working- hours, days, weeks or months;
//   - hours, days, weeks or months "of work", "of effort", "of development", "of engineering", "of coding", "of labour";
//   - "took" or "spent" followed by us, me, them, the team or the agents; "cost us", "cost me", "cost the project";
//   - "how long" with "took", "take" or "spent" in the same clause;
//   - an effort or cost field: "estimated hours", "actual effort", "elapsed time", estimatedHours, storyPoints;
//   - "cost", "costs", "costing", "spend" or "spent" with a currency or tokens within 30 characters;
//   - an amount of money: a currency sign with thousands separators, cents, or k/M/million/thousand, or a number with
//     USD, EUR, dollars or euros;
//   - the words budget, budgeted, pricing, price, priced, invoice, billing, billed, "hourly rate", "day rate", "token
//     usage", "API spend"; "overnight", "in one sitting", "in a single day/afternoon/evening/session".
//
// What it does not flag: the product's own times. COMMIT_RUN_DEADLINE_MINUTES, the 15-minute deadline of a
// reproduction run, the timeouts of the code and of `go test -timeout 120m`, a duration a test measures, dates: none
// of them is a work verb with a span, "took" with hours or more, or money. Stellar's own "fee" is not a cost of the
// work and is not a pattern.

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// conductSelf is this file: it holds the patterns and the statements that prove them, and is the one file the scan
// does not read.
const conductSelf = "cmd/ltk/conduct_test.go"

const (
	conductNumber = `(?:a|an|one|two|three|four|five|six|seven|eight|nine|ten|eleven|twelve|a few|a couple of|several|\d+(?:\.\d+)?)`
	conductUnit   = `(?:minutes?|hours?|days?|weeks?|months?|evenings?|afternoons?|nights?|weekends?|sprints?)`
)

// conductPatterns are the statements of how long the work took or what it cost (the header comment lists them).
var conductPatterns = []struct {
	name string
	re   *regexp.Regexp
}{
	{"a work verb with a span of time", regexp.MustCompile(`(?i)\b(?:built|written|wrote|developed|made|implemented|coded|finished|completed|delivered|spent|spending)\b[^.\n]{0,40}?\b(?:in|within|over|under|for|about|around|roughly|nearly|almost)\s+` + conductNumber + `\s+` + conductUnit + `\b`)},
	{"spent a span of time", regexp.MustCompile(`(?i)\b(?:spent|spending)\s+(?:about\s+|around\s+|roughly\s+|over\s+|nearly\s+|almost\s+)?` + conductNumber + `\s+` + conductUnit + `\b`)},
	{"took a span of hours or more", regexp.MustCompile(`(?i)\btook\b[^.\n]{0,20}?\b` + conductNumber + `\s+(?:hours?|days?|weeks?|months?|weekends?|evenings?|afternoons?|nights?)\b`)},
	{"a span of work", regexp.MustCompile(`(?i)\b` + conductNumber + `\s+(?:person|man|engineer|developer|working)[- ]?(?:hours?|days?|weeks?|months?)\b`)},
	{"units of work", regexp.MustCompile(`(?i)\b(?:hours?|days?|weeks?|months?)\s+of\s+(?:work|effort|development|engineering|coding|labou?r)\b`)},
	{"took or spent by someone", regexp.MustCompile(`(?i)\b(?:took|spent)\s+(?:us|me|them|the team|the agents?)\b`)},
	{"what it cost someone", regexp.MustCompile(`(?i)\bcost\s+(?:us|me|them|the project)\b`)},
	{"how long it took", regexp.MustCompile(`(?i)\bhow long\b[^.\n]{0,40}\b(?:took|take|spent)\b`)},
	{"an effort or cost field", regexp.MustCompile(`(?i)\b(?:estimated|actual|elapsed)[ _-]?(?:hours|effort|cost|time)\b|\bestimatedHours\b|\bstoryPoints\b`)},
	{"a cost in money or tokens", regexp.MustCompile(`(?i)\b(?:cost|costs|costing|spend|spent)\b[^.\n]{0,30}?(?:\$|USD|EUR|dollars?|euros?|€|£|tokens?)`)},
	{"an amount of money", regexp.MustCompile(`(?:\$|€|£)\s?\d{1,3}(?:,\d{3})+(?:\.\d+)?|(?:\$|€|£)\s?\d+(?:\.\d+)?\s?(?:k|K|m|M|million|thousand)\b|(?:\$|€|£)\s?\d+\.\d{2}\b|\b\d[\d,]*(?:\.\d+)?\s?(?:USD|EUR|dollars|euros)\b`)},
	{"budget or price", regexp.MustCompile(`(?i)\b(?:budget|budgets|budgeted|pricing|price[ds]?|invoice[ds]?|billing|billed|hourly rate|day rate|token usage|api spend)\b`)},
	{"overnight", regexp.MustCompile(`(?i)\b(?:overnight|in one sitting|in a single (?:day|afternoon|evening|session))\b`)},
}

// conductFindings returns one finding per match in text, "<line>: <pattern>: <match>".
func conductFindings(text string) []string {
	var f []string
	for i, line := range strings.Split(text, "\n") {
		for _, p := range conductPatterns {
			for _, m := range p.re.FindAllString(line, -1) {
				f = append(f, strings.Join([]string{strconv.Itoa(i + 1), p.name, m}, ": "))
			}
		}
	}
	return f
}

// TestNoFileSaysHowLongTheWorkTookOrWhatItCost: no file of the repository states how long
// the work took or what it cost.
func TestNoFileSaysHowLongTheWorkTookOrWhatItCost(t *testing.T) {
	scanned := 0
	for _, p := range gitFiles(t, true) {
		if isPackageOwn(p) || p == conductSelf {
			continue
		}
		b, err := os.ReadFile(filepath.Join(readmeModuleRoot, filepath.FromSlash(p)))
		if os.IsNotExist(err) {
			continue // tracked, deleted in the work tree
		}
		if err != nil {
			t.Fatal(err)
		}
		if bytes.IndexByte(b, 0) >= 0 {
			continue // binary
		}
		scanned++
		for _, f := range conductFindings(string(b)) {
			t.Errorf("%s:%s", p, f)
		}
	}
	if scanned == 0 {
		t.Fatal("no file was scanned")
	}
	t.Logf("%d files scanned outside the package's own entries", scanned)
}

// TestConductPatterns proves the scan is not vacuous and spares the product's own times.
func TestConductPatterns(t *testing.T) {
	for _, s := range []string{
		"This tool was built in two days.",
		"The README was written over a weekend.",
		"It took three days to build.",
		"It took us a while.",
		"Three person-weeks of effort.",
		"Two months of development.",
		"The API spend was $1,200.",
		"It cost $40.50 in tokens.",
		"Our budget was 5k.",
		"We spent 12 hours on it.",
		"Nobody asked how long it took.",
		"estimatedHours: 1.75",
		"It cost us nothing.",
		"done overnight",
	} {
		if len(conductFindings(s)) == 0 {
			t.Errorf("%q is not flagged", s)
		}
	}
	for _, s := range []string{
		"const COMMIT_RUN_DEADLINE_MINUTES = 15",
		"Each run of a commit is also given a safety deadline of 15 minutes.",
		"a run that reaches the deadline within 15 minutes",
		"go test -tags integration -p 1 -timeout 120m ./cmd/ltk/",
		"BuildTimeout is 60 minutes; ReadinessTimeout 5 minutes.",
		"The generation took 40 seconds and the check took 2 minutes.",
		"$ ltk list corpus/",
		`BIN="$RUNNER_TEMP/ltk"; "$BIN" $1`,
		`"fee_charged": "100", "fee_pool": "1234"`,
		"You may charge a fee for warranty support.",
		"2026-11-16T19:23:31.73323+00:00",
	} {
		if f := conductFindings(s); len(f) != 0 {
			t.Errorf("%q is flagged: %q", s, f)
		}
	}
}
