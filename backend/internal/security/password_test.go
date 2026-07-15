package security

import (
	"strings"
	"testing"
)

func TestPasswordRoundTrip(t *testing.T) {
	encoded, err := HashPassword("SafePassword123")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword("SafePassword123", encoded) {
		t.Fatal("new Argon2id hash did not verify")
	}
	if VerifyPassword("wrong", encoded) {
		t.Fatal("wrong password verified")
	}
}

func TestPasswordRejectsMalformedOrHostileParameters(t *testing.T) {
	for _, value := range []string{"", "$argon2i$v=19$m=65536,t=3,p=2$bad$bad", "$argon2id$v=19$m=999999999,t=3,p=2$MTIzNDU2Nzg5MDEyMzQ1Ng$MTIzNDU2Nzg5MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTI", "$argon2id$v=19$m=x,t=3,p=2$bad$bad"} {
		if VerifyPassword("password", value) {
			t.Fatalf("malformed hash accepted: %s", value)
		}
	}
}

func TestTokenAndCodeHashesAreDeterministicAndScoped(t *testing.T) {
	a := TokenHash("secret", "value")
	if len(a) != 64 || a != TokenHash("secret", "value") {
		t.Fatalf("unexpected token hash: %s", a)
	}
	if a == TokenHash("other", "value") {
		t.Fatal("secret did not scope token hash")
	}
	if CodeHash("secret", "USER@EXAMPLE.EDU", "register", "123456") != CodeHash("secret", "user@example.edu", "register", "123456") {
		t.Fatal("email normalization changed code hash")
	}
	if strings.Contains(CodeHash("secret", "user@example.edu", "register", "123456"), "123456") {
		t.Fatal("code leaked into hash")
	}
}

func TestRandomTokenUsesURLSafeEncoding(t *testing.T) {
	token, err := RandomToken(32)
	if err != nil {
		t.Fatal(err)
	}
	if len(token) < 40 || strings.ContainsAny(token, "+/=") {
		t.Fatalf("token is not raw URL-safe base64: %q", token)
	}
}
