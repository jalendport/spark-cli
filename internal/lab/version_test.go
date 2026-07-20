package lab

import "testing"

func TestResolveVersion(t *testing.T) {
	cases := []struct {
		in        string
		wantToken string
		wantMajor int
		wantPin   string
		wantErr   bool
	}{
		{"", "latest", 5, "", false},
		{"latest", "latest", 5, "", false},
		{"5", "5", 5, "", false},
		{"4", "4", 4, "", false},
		{"5.10", "5.10", 5, "5.10.*", false},
		{"4.16", "4.16", 4, "4.16.*", false},
		{"5.4.3", "5.4.3", 5, "5.4.3", false},
		{"3", "", 0, "", true},   // unsupported bare major
		{"3.2", "", 0, "", true}, // unsupported major, valid shape
		{"6.0", "", 0, "", true},
		{"nightly", "", 0, "", true},
		{"5.", "", 0, "", true},
		{"v5.10", "", 0, "", true},
		{"5.10.3.4", "", 0, "", true},
		{" 5.10", "", 0, "", true},
	}
	for _, c := range cases {
		token, major, pin, err := resolveVersion(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("resolveVersion(%q) = (%q,%d,%q), want error", c.in, token, major, pin)
			}
			continue
		}
		if err != nil {
			t.Errorf("resolveVersion(%q) unexpected error: %v", c.in, err)
			continue
		}
		if token != c.wantToken || major != c.wantMajor || pin != c.wantPin {
			t.Errorf("resolveVersion(%q) = (%q,%d,%q), want (%q,%d,%q)",
				c.in, token, major, pin, c.wantToken, c.wantMajor, c.wantPin)
		}
	}
}
