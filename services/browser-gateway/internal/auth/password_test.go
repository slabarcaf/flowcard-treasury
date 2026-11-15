package auth

import (
	"bytes"
	"strings"
	"testing"
)

func TestPasswordVerifier(t *testing.T) {
	password := "  fifteen unicode 界界  "
	p, err := NewPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	other, err := NewPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(p.Salt, other.Salt) || bytes.Equal(p.Verifier, other.Verifier) {
		t.Fatal("independent passwords reused salt or verifier")
	}
	if !Verify(password, p) || Verify(strings.TrimSpace(password), p) || Verify(password+"x", p) || Verify(password, Password{}) {
		t.Fatal("password verification accepted altered or unknown credentials")
	}
	p.Iterations++
	if Verify(password, p) {
		t.Fatal("unsupported work factor accepted")
	}
}

func TestPasswordBounds(t *testing.T) {
	for _, s := range []string{strings.Repeat("a", 15), strings.Repeat("界", 15), strings.Repeat(" ", 15), strings.Repeat("a", 1024)} {
		if !ValidPassword(s) {
			t.Errorf("valid password of %d bytes rejected", len(s))
		}
	}
	for _, s := range []string{strings.Repeat("a", 14), strings.Repeat("界", 14), strings.Repeat("a", 1025), strings.Repeat("a", 15) + "\xff"} {
		if ValidPassword(s) {
			t.Errorf("invalid password of %d bytes accepted", len(s))
		}
	}
	for _, name := range []string{"Bob", "ab", "user@host", "a\nxx", "a界xx"} {
		if ValidUsername(name) {
			t.Errorf("invalid username %q accepted", name)
		}
	}
}

func TestSessionTokens(t *testing.T) {
	token, digest, err := NewSession()
	if err != nil {
		t.Fatal(err)
	}
	got, ok := SessionDigest(token)
	if !ok || len(token) != 43 || len(digest) != 32 || !bytes.Equal(got, digest) {
		t.Fatal("session digest does not match canonical token")
	}
	for _, s := range []string{"", token + "=", strings.Repeat("!", 43), token[:42]} {
		if _, ok := SessionDigest(s); ok {
			t.Fatal("malformed token accepted")
		}
	}
}
