// Package manifest handles project detection and loading of the committed
// spark.yml that adapts the generic binary to a specific boilerplate.
package manifest

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Filename is the manifest the binary walks up the tree to find.
const Filename = "spark.yml"

// knownKeys are the top-level manifest keys the loader understands. Anything
// else triggers a warning rather than a failure, so newer manifests stay
// forward-compatible with older binaries.
var knownKeys = map[string]bool{
	"name":        true,
	"description": true,
	"commands":    true,
	"create":      true,
}

// Command is a single manifest-defined command. It supports two YAML forms:
// a bare string (shell one-liner) or a map with run/help fields.
type Command struct {
	Name string
	Run  string
	Help string
}

// Create mirrors the create-time setup schema. v1 parses it (so malformed
// sections surface early) but does not act on it — implementation is deferred.
type Create struct {
	Prompts []struct {
		Key   string `yaml:"key"`
		Label string `yaml:"label"`
	} `yaml:"prompts"`
	Rename  map[string]string `yaml:"rename"`
	Replace map[string]string `yaml:"replace"`
	Post    []string          `yaml:"post"`

	// Setup lists shell commands run from the project directory once it has
	// already been moved into its final location — after scaffolding finishes,
	// behind a confirmation prompt. This is deliberately distinct from Post,
	// which runs in the pre-rename temporary build directory: setup commands
	// may start docker compose, and compose derives its project name and
	// bind-mount paths from the working directory, so they must only ever run
	// with the project sitting in its real home, never in the temp dir.
	Setup []string `yaml:"setup"`
}

// Manifest is a loaded spark.yml plus the resolved project root.
type Manifest struct {
	Name        string
	Description string
	Commands    []Command // ordered as written, for stable help output
	Create      *Create   // parsed and ignored in v1

	Root        string   // absolute path of the directory containing spark.yml
	UnknownKeys []string // top-level keys we didn't recognize
}

// Lookup returns the manifest command with the given name, if any.
func (m *Manifest) Lookup(name string) (Command, bool) {
	for _, c := range m.Commands {
		if c.Name == name {
			return c, true
		}
	}
	return Command{}, false
}

// FindRoot walks up from start looking for spark.yml. It returns the directory
// containing the manifest and true, or "" and false when no project is found.
func FindRoot(start string) (string, bool) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", false
	}
	for {
		if info, err := os.Stat(filepath.Join(dir, Filename)); err == nil && !info.IsDir() {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir { // reached the filesystem root
			return "", false
		}
		dir = parent
	}
}

// Load reads and parses the manifest at root/spark.yml.
func Load(root string) (*Manifest, error) {
	data, err := os.ReadFile(filepath.Join(root, Filename))
	if err != nil {
		return nil, err
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", Filename, err)
	}

	m := &Manifest{Root: root}

	// An empty manifest is valid — the built-in verbs still apply.
	if len(doc.Content) == 0 {
		return m, nil
	}
	top := doc.Content[0]
	if top.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s: expected a mapping at the top level", Filename)
	}

	// Mapping nodes store keys and values as alternating children.
	for i := 0; i+1 < len(top.Content); i += 2 {
		key := top.Content[i]
		val := top.Content[i+1]
		switch key.Value {
		case "name":
			m.Name = val.Value
		case "description":
			m.Description = val.Value
		case "commands":
			cmds, err := parseCommands(val)
			if err != nil {
				return nil, err
			}
			m.Commands = cmds
		case "create":
			var c Create
			if err := val.Decode(&c); err != nil {
				return nil, fmt.Errorf("%s: create: %w", Filename, err)
			}
			m.Create = &c
		default:
			if !knownKeys[key.Value] {
				m.UnknownKeys = append(m.UnknownKeys, key.Value)
			}
		}
	}
	return m, nil
}

// parseCommands turns the commands mapping into an ordered slice, accepting
// both the string form and the map form for each entry.
func parseCommands(node *yaml.Node) ([]Command, error) {
	if node.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s: commands: expected a mapping", Filename)
	}
	var out []Command
	for i := 0; i+1 < len(node.Content); i += 2 {
		name := node.Content[i].Value
		val := node.Content[i+1]
		cmd := Command{Name: name}
		switch val.Kind {
		case yaml.ScalarNode:
			cmd.Run = val.Value
		case yaml.MappingNode:
			var raw struct {
				Run  string `yaml:"run"`
				Help string `yaml:"help"`
			}
			if err := val.Decode(&raw); err != nil {
				return nil, fmt.Errorf("%s: commands.%s: %w", Filename, name, err)
			}
			cmd.Run = raw.Run
			cmd.Help = raw.Help
		default:
			return nil, fmt.Errorf("%s: commands.%s: expected a string or mapping", Filename, name)
		}
		if cmd.Run == "" {
			return nil, fmt.Errorf("%s: commands.%s: missing run command", Filename, name)
		}
		out = append(out, cmd)
	}
	return out, nil
}
