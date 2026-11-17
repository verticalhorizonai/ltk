package main

// The CLI router in process: parsing with a registry of test
// commands shaped like ltk's own, so the parsing rules are held apart from any command's own work.

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"slices"
	"strings"
	"testing"

	"github.com/verticalhorizonai/ltk/internal/contract"
	"github.com/verticalhorizonai/ltk/internal/model"
)

// ran records one run of a test command.
type ran struct {
	name  string
	args  []string
	flags map[string]string
}

// testRegistry registers `list` (<folder> [--json]), `compare` (<folder A> <folder B>), `generate` (--all, --shape,
// --out) and `repro js-sdk-1766`, each recording its run into *got.
func testRegistry(got *ran) *registry {
	reg := emptyRegistry()
	add := func(name string, synopsis []string, positionals []string, declare func(fs *flag.FlagSet)) {
		reg.register(&command{name: name, synopsis: synopsis, positionals: positionals,
			setup: func(fs *flag.FlagSet) func(context.Context, invocation) error {
				declare(fs)
				return func(_ context.Context, inv invocation) error {
					*got = ran{name: name, args: inv.args, flags: map[string]string{}}
					fs.Visit(func(f *flag.Flag) { got.flags[f.Name] = f.Value.String() })
					return nil
				}
			}})
	}
	add("list", []string{"ltk list <folder> [--json]"}, []string{"<folder>"}, func(fs *flag.FlagSet) { fs.Bool("json", false, "") })
	add("compare", []string{"ltk compare <folder A> <folder B>"}, []string{"<folder A>", "<folder B>"}, func(*flag.FlagSet) {})
	add("generate", []string{"ltk generate --all --out <folder>", "ltk generate --shape <entry id> --out <folder>"}, nil,
		func(fs *flag.FlagSet) {
			fs.Bool("all", false, "")
			fs.String("shape", "", "")
			fs.String("out", "", "")
		})
	add("repro js-sdk-1766", []string{"ltk repro js-sdk-1766"}, nil, func(*flag.FlagSet) {})
	return reg
}

func usageCode(err error) contract.ErrorCode {
	var c *model.CodedError
	if errors.As(err, &c) {
		return c.Code
	}
	return ""
}

func TestRouterParsing(t *testing.T) {
	cases := []struct {
		args  []string
		name  string
		pos   []string
		flags map[string]string
	}{
		{[]string{"list", "corpus/"}, "list", []string{"corpus/"}, map[string]string{}},
		{[]string{"list", "corpus/", "--json"}, "list", []string{"corpus/"}, map[string]string{"json": "true"}},
		{[]string{"list", "--json", "corpus/"}, "list", []string{"corpus/"}, map[string]string{"json": "true"}},
		{[]string{"list", "-json", "corpus/"}, "list", []string{"corpus/"}, map[string]string{"json": "true"}},
		{[]string{"list", "corpus/", "--json=false"}, "list", []string{"corpus/"}, map[string]string{"json": "false"}},
		{[]string{"list", "--", "--json"}, "list", []string{"--json"}, map[string]string{}},
		{[]string{"list", "--json", "--", "-"}, "list", []string{"-"}, map[string]string{"json": "true"}},
		{[]string{"compare", "a", "b"}, "compare", []string{"a", "b"}, map[string]string{}},
		{[]string{"generate", "--all", "--out", "x"}, "generate", nil, map[string]string{"all": "true", "out": "x"}},
		{[]string{"generate", "--out=x", "--shape", "e1"}, "generate", nil, map[string]string{"out": "x", "shape": "e1"}},
		{[]string{"generate", "--shape", "--", "--out", "y"}, "generate", nil, map[string]string{"shape": "--", "out": "y"}},
		{[]string{"repro", "js-sdk-1766"}, "repro js-sdk-1766", nil, map[string]string{}},
	}
	for _, c := range cases {
		var got ran
		var out bytes.Buffer
		if err := dispatch(context.Background(), testRegistry(&got), c.args, &out); err != nil {
			t.Errorf("%q: %v", c.args, err)
			continue
		}
		if got.name != c.name || !slices.Equal(got.args, c.pos) || len(got.flags) != len(c.flags) {
			t.Errorf("%q: ran %+v, want %s %q %v", c.args, got, c.name, c.pos, c.flags)
			continue
		}
		for k, v := range c.flags {
			if got.flags[k] != v {
				t.Errorf("%q: flag %s = %q, want %q", c.args, k, got.flags[k], v)
			}
		}
		if out.Len() != 0 {
			t.Errorf("%q: the router wrote %q", c.args, out.String())
		}
	}
}

func TestRouterUsageErrors(t *testing.T) {
	cases := []struct {
		args     []string
		contains string
	}{
		{nil, "no command given"},
		{[]string{"frobnicate"}, `unknown command "frobnicate"`},
		{[]string{"help"}, `unknown command "help"`},
		{[]string{"--bogus"}, `unknown command "--bogus"`},
		{[]string{"check", "corpus/"}, `"ltk check" is not available`},
		{[]string{"repro", "wallet-backend-729"}, `"ltk repro wallet-backend-729" is not available`},
		{[]string{"repro"}, "ltk repro needs a target: wallet-backend-729 or js-sdk-1766"},
		{[]string{"repro", "nope"}, `unknown reproduction target "nope"`},
		{[]string{"list", "corpus/", "--bogus"}, "flag provided but not defined: -bogus; usage: ltk list <folder> [--json]"},
		{[]string{"list"}, "missing argument <folder>"},
		{[]string{"list", "a", "b"}, `unexpected argument "b"`},
		{[]string{"compare", "a"}, "missing argument <folder B>"},
		{[]string{"generate", "--out"}, "flag needs an argument: -out"},
		{[]string{"generate", "x"}, `unexpected argument "x"; usage: ltk generate --all --out <folder> | ltk generate --shape <entry id> --out <folder>`},
		{[]string{"repro", "js-sdk-1766", "extra"}, `unexpected argument "extra"`},
		{[]string{"list", "--json=maybe", "a"}, "invalid boolean value"},
	}
	for _, c := range cases {
		var got ran
		var out bytes.Buffer
		err := dispatch(context.Background(), testRegistry(&got), c.args, &out)
		if usageCode(err) != contract.ErrorCodeUsageError {
			t.Errorf("%q: %v, want a usage error", c.args, err)
			continue
		}
		if !strings.Contains(err.Error(), c.contains) {
			t.Errorf("%q: message %q does not contain %q", c.args, err.Error(), c.contains)
		}
		if got.name != "" || out.Len() != 0 {
			t.Errorf("%q: a usage error must run nothing and write nothing to stdout; ran %+v, wrote %q", c.args, got, out.String())
		}
	}
}

func TestRouterHelp(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"list", "--help"}, "ltk list <folder> [--json]\n"},
		{[]string{"list", "-h"}, "ltk list <folder> [--json]\n"},
		{[]string{"list", "corpus/", "--help"}, "ltk list <folder> [--json]\n"},
		{[]string{"generate", "--help"}, "ltk generate --all --out <folder>\nltk generate --shape <entry id> --out <folder>\n"},
		{[]string{"repro", "js-sdk-1766", "--help"}, "ltk repro js-sdk-1766\n"},
		{[]string{"repro", "--help"}, invocationLine + "\nltk repro js-sdk-1766\n"},
		{[]string{"--help"}, invocationLine + "\nltk generate --all --out <folder>\nltk generate --shape <entry id> --out <folder>\n" +
			"ltk compare <folder A> <folder B>\nltk list <folder> [--json]\nltk repro js-sdk-1766\n"},
		{[]string{"-h"}, invocationLine + "\nltk generate --all --out <folder>\nltk generate --shape <entry id> --out <folder>\n" +
			"ltk compare <folder A> <folder B>\nltk list <folder> [--json]\nltk repro js-sdk-1766\n"},
	}
	for _, c := range cases {
		var got ran
		var out bytes.Buffer
		if err := dispatch(context.Background(), testRegistry(&got), c.args, &out); err != nil {
			t.Errorf("%q: %v", c.args, err)
			continue
		}
		if out.String() != c.want {
			t.Errorf("%q: stdout %q, want %q", c.args, out.String(), c.want)
		}
		if got.name != "" {
			t.Errorf("%q: help ran the command", c.args)
		}
	}
	// The program's registry: `ltk --help` is the invocation line, then the registered commands' synopses in the
	// order of contractCommands; every command is registered, so it is every command's synopsis, word for word.
	var out bytes.Buffer
	want := invocationLine + "\nltk generate --all --out <folder>\nltk generate --shape <entry id> --out <folder>" +
		"\nltk compare <folder A> <folder B>\nltk repro wallet-backend-729\nltk list <folder> [--json]" +
		"\nltk check <folder> [--also-go]\nltk repro js-sdk-1766\n"
	if err := dispatch(context.Background(), newRegistry(), []string{"--help"}, &out); err != nil || out.String() != want {
		t.Errorf("ltk --help: %v %q, want %q", err, out.String(), want)
	}
}

func TestRegisterRejectsDefects(t *testing.T) {
	for _, c := range []*command{{name: "frobnicate"}, {name: "list"}} {
		func() {
			reg := emptyRegistry()
			reg.register(&command{name: "list"})
			defer func() {
				if recover() == nil {
					t.Errorf("registering %q must panic", c.name)
				}
			}()
			reg.register(c)
		}()
	}
}

func TestDispatchReturnsTheCommandsError(t *testing.T) {
	reg := emptyRegistry()
	want := model.Failure("check failed")
	reg.register(&command{name: "check", synopsis: []string{"ltk check <folder> [--also-go]"}, positionals: []string{"<folder>"},
		setup: func(*flag.FlagSet) func(context.Context, invocation) error {
			return func(context.Context, invocation) error { return want }
		}})
	if err := dispatch(context.Background(), reg, []string{"check", "corpus/"}, &bytes.Buffer{}); !errors.Is(err, want) {
		t.Errorf("got %v, want the command's own error", err)
	}
}
