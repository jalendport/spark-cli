package cli

import (
	"reflect"
	"testing"
)

func TestFreeAliases(t *testing.T) {
	taken := map[string]bool{"start": true}

	if got := freeAliases([]string{"u", "start"}, taken); !reflect.DeepEqual(got, []string{"u"}) {
		t.Errorf("freeAliases dropped wrong aliases: %v", got)
	}
	if got := freeAliases([]string{"u", "start"}, nil); !reflect.DeepEqual(got, []string{"u", "start"}) {
		t.Errorf("freeAliases with nothing taken = %v", got)
	}
	if got := freeAliases(nil, taken); got != nil {
		t.Errorf("freeAliases(nil) = %v, want nil", got)
	}
}

func TestApplyArgs(t *testing.T) {
	cases := []struct {
		run  string
		args []string
		want string
	}{
		{"echo hi {args} bye", []string{"a", "b c"}, "echo hi a 'b c' bye"},
		{"echo hi", []string{"x"}, "echo hi x"},
		{"echo hi", nil, "echo hi"},
		{"echo {args}", []string{">/tmp/x"}, "echo '>/tmp/x'"},
		{"echo {args}", []string{"it's"}, `echo 'it'\''s'`},
	}
	for _, c := range cases {
		if got := applyArgs(c.run, c.args); got != c.want {
			t.Errorf("applyArgs(%q, %v) = %q, want %q", c.run, c.args, got, c.want)
		}
	}
}
