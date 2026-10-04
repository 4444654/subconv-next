package authn

import (
	"strings"
	"testing"
)

func TestPasswordHashingUsesExactPasswordAndRejectsTruncation(t *testing.T) {
	password := "  independent passphrase  "
	hash, err := HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	if hash == password || ValidateHash(hash) != nil || !VerifyPassword(hash, password) {
		t.Fatal("valid password did not round-trip")
	}
	for _, wrong := range []string{strings.TrimSpace(password), "wrong password", password + strings.Repeat("x", 73)} {
		if VerifyPassword(hash, wrong) {
			t.Fatal("incorrect or oversized password accepted")
		}
	}
	for _, invalid := range []string{"", "short", strings.Repeat("x", 73)} {
		if _, err := HashPassword(invalid); err == nil {
			t.Fatal("invalid password accepted")
		}
	}
	if ValidateHash("not-a-hash") == nil {
		t.Fatal("malformed hash accepted")
	}
}
