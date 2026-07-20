package lab

import (
	"strings"
	"testing"
)

// validMeta is a baseline that must pass validate; each case breaks one field.
func validMeta() instanceMeta {
	return instanceMeta{
		Name:         "latest-mysql",
		Plugin:       "jalendport/craft-example",
		Handle:       "example",
		CraftMajor:   5,
		CraftVersion: "latest",
		PHPTag:       "8.2",
		DB:           "mysql",
		WebPort:      8100,
		MailpitPort:  8101,
	}
}

func TestValidateMeta(t *testing.T) {
	dir := "/plugins/x/.lab/latest-mysql"

	if err := validMeta().validate(dir); err != nil {
		t.Fatalf("baseline meta rejected: %v", err)
	}

	cases := []struct {
		label  string
		mutate func(*instanceMeta)
		errHas string
	}{
		{"name/dir mismatch", func(m *instanceMeta) { m.Name = "other" }, "does not match"},
		{"traversal name", func(m *instanceMeta) { m.Name = "../evil" }, "does not match"},
		{"unknown db", func(m *instanceMeta) { m.DB = "sqlite" }, "unknown db"},
		{"zero port", func(m *instanceMeta) { m.WebPort = 0 }, "out-of-range"},
		{"huge port", func(m *instanceMeta) { m.MailpitPort = 70000 }, "out-of-range"},
		{"missing plugin", func(m *instanceMeta) { m.Plugin = "" }, "plugin identity"},
		{"missing handle", func(m *instanceMeta) { m.Handle = "" }, "plugin identity"},
		{"unsupported major", func(m *instanceMeta) { m.CraftMajor = 3 }, "unsupported Craft major"},
		{"missing craft version", func(m *instanceMeta) { m.CraftVersion = "" }, "version info"},
		{"missing php tag", func(m *instanceMeta) { m.PHPTag = "" }, "version info"},
	}
	for _, c := range cases {
		m := validMeta()
		c.mutate(&m)
		err := m.validate(dir)
		if err == nil {
			t.Errorf("%s: validate accepted a broken meta", c.label)
			continue
		}
		if !strings.Contains(err.Error(), c.errHas) {
			t.Errorf("%s: error %q does not mention %q", c.label, err, c.errHas)
		}
	}
}
