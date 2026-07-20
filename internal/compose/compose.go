// Package compose knows how to locate a Compose file at the project root and
// build the default docker compose invocations for the built-in verbs.
package compose

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// candidates are the Compose filenames we recognize, in precedence order.
var candidates = []string{"docker-compose.yml", "docker-compose.yaml", "compose.yaml", "compose.yml"}

// File returns the path to the Compose file at root, and whether one exists.
func File(root string) (string, bool) {
	for _, name := range candidates {
		p := filepath.Join(root, name)
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p, true
		}
	}
	return "", false
}

// Base is the command prefix every default verb builds on.
func Base() []string {
	return []string{"docker", "compose"}
}

// FirstService reads the Compose file and returns the first service name in
// declaration order, used as the default target for `spark sh`.
func FirstService(root string) (string, error) {
	path, ok := File(root)
	if !ok {
		return "", fmt.Errorf("no compose file found")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return "", fmt.Errorf("parse %s: %w", filepath.Base(path), err)
	}
	if len(doc.Content) == 0 {
		return "", fmt.Errorf("%s: empty compose file", filepath.Base(path))
	}

	// Find the `services` mapping and return the first key in file order.
	top := doc.Content[0]
	for i := 0; i+1 < len(top.Content); i += 2 {
		if top.Content[i].Value != "services" {
			continue
		}
		services := top.Content[i+1]
		if services.Kind == yaml.MappingNode && len(services.Content) > 0 {
			return services.Content[0].Value, nil
		}
	}
	return "", fmt.Errorf("%s: no services defined", filepath.Base(path))
}
