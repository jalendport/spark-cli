package lab

import (
	"encoding/json"
	"fmt"
	"os"
)

// parseComposer decodes a composer.json into a generic map.
func parseComposer(data []byte) (map[string]any, error) {
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// stringField returns m[key] as a string, or "" if absent or not a string.
func stringField(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// pluginHandle reads extra.handle from a parsed composer.json.
func pluginHandle(m map[string]any) string {
	extra, ok := m["extra"].(map[string]any)
	if !ok {
		return ""
	}
	return stringField(extra, "handle")
}

// rewriteComposer applies the mint-time edits to the instance composer.json
// (see ENGINE.md §4): asset-packagist + a symlinked path repo for the plugin,
// the plugin required at "*", an optional exact Craft pin, and the platform PHP
// version. The lab module's PSR-4 mapping ships in the skeleton, so it is left
// untouched here.
func rewriteComposer(path, pluginName, pluginVersion, craftVersion, phpTag string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	m, err := parseComposer(data)
	if err != nil {
		return fmt.Errorf("parse instance composer.json: %w", err)
	}

	// repositories: asset-packagist (bower-asset/* for yii2-redis) + a symlinked
	// path repo for the plugin, version-pinned so inter-package constraints
	// resolve against a branch/tag checkout.
	m["repositories"] = []any{
		map[string]any{"type": "composer", "url": "https://asset-packagist.org"},
		map[string]any{
			"type": "path",
			"url":  "/plugin",
			"options": map[string]any{
				"symlink":  true,
				"versions": map[string]any{pluginName: pluginVersion},
			},
		},
	}

	require, _ := m["require"].(map[string]any)
	if require == nil {
		require = map[string]any{}
	}
	require[pluginName] = "*"
	if craftVersion != "" {
		require["craftcms/cms"] = craftVersion
	}
	m["require"] = require

	// config.platform.php pins the resolver's PHP version to the image's.
	config, _ := m["config"].(map[string]any)
	if config == nil {
		config = map[string]any{}
	}
	platform, _ := config["platform"].(map[string]any)
	if platform == nil {
		platform = map[string]any{}
	}
	platform["php"] = phpTag
	config["platform"] = platform
	m["config"] = config

	return writeJSON(path, m)
}

// writeJSON marshals v with tab indentation and a trailing newline, matching
// the reference implementation's composer.json formatting.
func writeJSON(path string, v any) error {
	out, err := json.MarshalIndent(v, "", "\t")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	return os.WriteFile(path, out, 0o644)
}
