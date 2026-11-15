package cli

import (
	"strings"
	"testing"
)

func TestPasswordStdin(t *testing.T) {
	password := "  preserve this password  "
	for _, suffix := range []string{"", "\n", "\r\n"} {
		got, err := ReadPassword(strings.NewReader(password + suffix))
		if err != nil || got != password {
			t.Errorf("ReadPassword changed terminal input with suffix %q", suffix)
		}
	}
	got, err := ReadPassword(strings.NewReader(password + "\n\n"))
	if err != nil || got != password+"\n" {
		t.Fatal("ReadPassword stripped more than one newline")
	}
	for _, s := range []string{"short", strings.Repeat("a", 1025), strings.Repeat("a", 1024) + "\n\n"} {
		if _, err := ReadPassword(strings.NewReader(s)); err == nil {
			t.Fatal("invalid stdin password accepted")
		}
	}
}
