package create

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// registryURL is the canonical published boilerplate registry. `spark create`
// fetches it to power the picker and to resolve a named boilerplate.
const registryURL = "https://raw.githubusercontent.com/jalendport/spark-cli/master/registry.json"

// maxRegistryBytes caps the registry response so a hostile or misbehaving
// endpoint can't stream an unbounded body into memory. 1MB is ample.
const maxRegistryBytes = 1 << 20

// Boilerplate is one registry entry: a named, public GitHub repo that
// `spark create` can scaffold from.
type Boilerplate struct {
	Name        string `json:"name"`
	Repo        string `json:"repo"`
	Description string `json:"description"`
}

// Registry is the parsed registry.json document.
type Registry struct {
	Boilerplates []Boilerplate `json:"boilerplates"`
}

// find returns the boilerplate whose name matches, case-insensitively.
func (r Registry) find(name string) (Boilerplate, bool) {
	for _, b := range r.Boilerplates {
		if strings.EqualFold(b.Name, name) {
			return b, true
		}
	}
	return Boilerplate{}, false
}

// names returns the boilerplate names in registry order, for error listings.
func (r Registry) names() []string {
	out := make([]string, len(r.Boilerplates))
	for i, b := range r.Boilerplates {
		out[i] = b.Name
	}
	return out
}

// parseRegistry decodes registry JSON, rejecting a document with no entries so
// a truncated or wrong-shaped file fails clearly instead of yielding an empty
// picker.
func parseRegistry(data []byte) (Registry, error) {
	var reg Registry
	if err := json.Unmarshal(data, &reg); err != nil {
		return Registry{}, fmt.Errorf("parse registry: %w", err)
	}
	if len(reg.Boilerplates) == 0 {
		return Registry{}, fmt.Errorf("registry lists no boilerplates")
	}
	return reg, nil
}

// fetchRegistry downloads and parses the published registry, degrading with a
// clear message when the network or the endpoint is unavailable.
func fetchRegistry() (Registry, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(registryURL)
	if err != nil {
		return Registry{}, fmt.Errorf("could not reach the boilerplate registry at %s: %w", registryURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Registry{}, fmt.Errorf("boilerplate registry at %s returned %s", registryURL, resp.Status)
	}
	data, err := readLimited(resp.Body, maxRegistryBytes)
	if err != nil {
		return Registry{}, fmt.Errorf("read registry response: %w", err)
	}
	return parseRegistry(data)
}

// readLimited reads all of r but refuses a source larger than max bytes, so an
// unbounded response can't exhaust memory.
func readLimited(r io.Reader, max int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("response exceeds the %d-byte limit", max)
	}
	return data, nil
}
