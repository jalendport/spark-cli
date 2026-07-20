package proc

import (
	"errors"
	"fmt"
	"testing"
)

func TestQuote(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{[]string{"plain"}, "plain"},
		{[]string{"two words"}, "'two words'"},
		{[]string{""}, "''"},
		{[]string{">/tmp/out"}, "'>/tmp/out'"},
		{[]string{"$HOME", "a;b", "`id`"}, "'$HOME' 'a;b' '`id`'"},
		{[]string{"it's"}, `'it'\''s'`},
		{[]string{"a", "b"}, "a b"},
	}
	for _, c := range cases {
		if got := Quote(c.in); got != c.want {
			t.Errorf("Quote(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCode(t *testing.T) {
	if code, ok := Code(&ExitError{Code: 7}); !ok || code != 7 {
		t.Errorf("Code(ExitError{7}) = (%d, %v)", code, ok)
	}
	if code, ok := Code(fmt.Errorf("wrapped: %w", &ExitError{Code: 3})); !ok || code != 3 {
		t.Errorf("Code(wrapped ExitError{3}) = (%d, %v)", code, ok)
	}
	if _, ok := Code(errors.New("plain")); ok {
		t.Error("Code(plain error) reported ok")
	}
}
