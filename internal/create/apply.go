package create

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jalendport/spark-cli/internal/manifest"
	"github.com/jalendport/spark-cli/internal/proc"
	"github.com/jalendport/spark-cli/internal/ui"
)

// skipDirs are directories the replace pass never descends into: dependency
// trees are large, binary-heavy, and not the boilerplate's own source.
var skipDirs = map[string]bool{
	".git":         true,
	"vendor":       true,
	"node_modules": true,
}

// executeCreate runs the manifest's create: section against an already-extracted
// project at root: interactive prompts, file renames, token replacement, and
// post shell commands. rd carries the prompt input and out the dialogue, so
// tests can drive it without a terminal. Git initialization is the caller's
// job (see setupProject) so it happens even without a create: section.
func executeCreate(root string, c *manifest.Create, rd *bufio.Reader, out io.Writer) error {
	answers, err := runPrompts(c, rd, out)
	if err != nil {
		return err
	}
	if err := applyRename(root, c.Rename); err != nil {
		return err
	}
	if err := applyReplace(root, c.Replace, answers); err != nil {
		return err
	}
	return runPost(root, c.Post)
}

// runPrompts asks each configured prompt in order and returns the answers keyed
// by prompt key. An empty prompt list yields an empty map.
func runPrompts(c *manifest.Create, rd *bufio.Reader, out io.Writer) (map[string]string, error) {
	answers := map[string]string{}
	for _, p := range c.Prompts {
		label := p.Label
		if label == "" {
			label = p.Key
		}
		fmt.Fprint(out, ui.CommandName.Render(label)+ui.Dim.Render(" › "))
		line, err := rd.ReadString('\n')
		if err != nil && err != io.EOF {
			return nil, err
		}
		// A trailing answer without a newline still counts; but input that runs
		// out before a prompt gets any text must not be treated as blank.
		if err == io.EOF && line == "" {
			return nil, fmt.Errorf("input ended before all prompts were answered")
		}
		answers[p.Key] = strings.TrimSpace(line)
	}
	return answers, nil
}

// applyRename moves each source path to its destination, both relative to root.
// It lets a boilerplate ship a template file under a name that would otherwise
// conflict, replacing the shipped placeholder (e.g. composer.json.project ->
// composer.json). Renames run in sorted key order for determinism, and the whole
// set is validated first so os.Rename's silent overwrite can't produce an
// order-dependent result: two renames may not target the same destination, and a
// destination may not be another rename's source (which order would clobber).
// Overwriting an ordinary shipped file is allowed — that is the feature's point.
func applyRename(root string, rename map[string]string) error {
	srcs := make([]string, 0, len(rename))
	for src := range rename {
		srcs = append(srcs, src)
	}
	sort.Strings(srcs)

	sources := make(map[string]string, len(rename)) // abs source path -> source key
	for _, src := range srcs {
		sources[filepath.Join(root, src)] = src
	}

	claimed := make(map[string]string, len(rename)) // dest path -> source claiming it
	for _, src := range srcs {
		dst := rename[src]
		from := filepath.Join(root, src)
		to := filepath.Join(root, dst)
		if !within(root, from) || !within(root, to) {
			return fmt.Errorf("rename %q → %q escapes the project directory", src, dst)
		}
		if _, err := os.Stat(from); err != nil {
			return fmt.Errorf("rename source %q does not exist", src)
		}
		if prev, ok := claimed[to]; ok {
			return fmt.Errorf("renames %q and %q both target %q", prev, src, dst)
		}
		claimed[to] = src
		if other, ok := sources[to]; ok && other != src {
			return fmt.Errorf("rename %q → %q would overwrite %q, which is itself being renamed", src, dst, other)
		}
	}

	for _, src := range srcs {
		dst := rename[src]
		from := filepath.Join(root, src)
		to := filepath.Join(root, dst)
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return err
		}
		if err := os.Rename(from, to); err != nil {
			return fmt.Errorf("rename %q → %q: %w", src, dst, err)
		}
		ui.Stepf("renamed %s → %s", src, dst)
	}
	return nil
}

// applyReplace substitutes each token with its resolved replacement across every
// text file under root, skipping binaries and dependency directories. A
// replacement value may reference prompt answers via {key} placeholders.
func applyReplace(root string, replace map[string]string, answers map[string]string) error {
	if len(replace) == 0 {
		return nil
	}
	resolved := make(map[string]string, len(replace))
	for token, tpl := range replace {
		if token == "" {
			// An empty token would splice the replacement between every character
			// via strings.ReplaceAll — skip it rather than corrupt every file.
			ui.Warnf("ignoring empty replace token in %s", manifest.Filename)
			continue
		}
		resolved[token] = expand(tpl, answers)
	}
	if len(resolved) == 0 {
		return nil
	}

	// Build one single-pass replacer with the longest tokens first. This makes
	// the substitution deterministic regardless of map order and prevents
	// cascading, where one token's replacement is itself rewritten by another
	// (e.g. A→B and B→C must not turn A into C).
	tokens := make([]string, 0, len(resolved))
	for token := range resolved {
		tokens = append(tokens, token)
	}
	sort.Slice(tokens, func(i, j int) bool {
		if len(tokens[i]) != len(tokens[j]) {
			return len(tokens[i]) > len(tokens[j])
		}
		return tokens[i] < tokens[j]
	})
	pairs := make([]string, 0, len(tokens)*2)
	for _, token := range tokens {
		pairs = append(pairs, token, resolved[token])
	}
	replacer := strings.NewReplacer(pairs...)

	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if path != root && skipDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if isBinary(data) {
			return nil
		}
		out := replacer.Replace(string(data))
		if out == string(data) {
			return nil
		}
		return os.WriteFile(path, []byte(out), info.Mode().Perm())
	})
}

// runPost executes each post shell command via `sh -c` from the project root,
// echoing it first and passing through stdio.
func runPost(root string, post []string) error {
	for _, script := range post {
		ui.Echo(script)
		cmd := exec.Command("sh", "-c", script)
		cmd.Dir = root
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			// A command that ran and exited nonzero should pass its real code
			// through to the CLI; only a failure to start is a plain error.
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				return &proc.ExitError{Code: ee.ExitCode()}
			}
			return fmt.Errorf("post command failed (%s): %w", script, err)
		}
	}
	return nil
}

// gitInit initializes a fresh git repository at root without making an initial
// commit, so the developer owns the first commit.
func gitInit(root string) error {
	ui.Echo("git init")
	cmd := exec.Command("git", "init")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git init: %w\n%s", err, out)
	}
	return nil
}

// expand resolves {key} placeholders in tpl against the prompt answers.
func expand(tpl string, answers map[string]string) string {
	out := tpl
	for k, v := range answers {
		out = strings.ReplaceAll(out, "{"+k+"}", v)
	}
	return out
}

// isBinary reports whether data looks non-textual, detected by a NUL byte in
// the leading window — the same heuristic git uses.
func isBinary(data []byte) bool {
	n := len(data)
	if n > 8000 {
		n = 8000
	}
	return bytes.IndexByte(data[:n], 0) >= 0
}
