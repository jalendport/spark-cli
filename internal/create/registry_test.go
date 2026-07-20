package create

import (
	"strings"
	"testing"
)

func TestParseRegistry(t *testing.T) {
	data := []byte(`{"boilerplates":[
		{"name":"craft","repo":"jalendport/spark-craft","description":"Craft CMS boilerplate"}
	]}`)
	reg, err := parseRegistry(data)
	if err != nil {
		t.Fatalf("parseRegistry: %v", err)
	}
	if len(reg.Boilerplates) != 1 {
		t.Fatalf("got %d boilerplates, want 1", len(reg.Boilerplates))
	}
	b := reg.Boilerplates[0]
	if b.Name != "craft" || b.Repo != "jalendport/spark-craft" {
		t.Errorf("unexpected entry: %+v", b)
	}

	if _, ok := reg.find("CRAFT"); !ok {
		t.Error("find should match case-insensitively")
	}
	if _, ok := reg.find("missing"); ok {
		t.Error("find matched a name that isn't present")
	}
}

func TestParseRegistryErrors(t *testing.T) {
	if _, err := parseRegistry([]byte("not json")); err == nil {
		t.Error("malformed JSON accepted")
	}
	if _, err := parseRegistry([]byte(`{"boilerplates":[]}`)); err == nil {
		t.Error("empty registry accepted")
	}
}

func TestReadLimited(t *testing.T) {
	got, err := readLimited(strings.NewReader("hello"), 10)
	if err != nil {
		t.Fatalf("readLimited under the limit: %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("readLimited returned %q", got)
	}
	if _, err := readLimited(strings.NewReader(strings.Repeat("x", 100)), 10); err == nil {
		t.Fatal("expected an over-limit reader to be rejected")
	}
}
