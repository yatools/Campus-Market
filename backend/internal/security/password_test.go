package security

import "testing"

func TestPythonArgon2HashCompatibility(t *testing.T) {
	const pythonHash = "$argon2id$v=19$m=65536,t=3,p=2$iJYRbF+38qC9Fm6LjTShRQ$qGaSFqypYLsdYk2HLB9rTqk5NI/IXm4seF8nk5yR5Fk"
	if !VerifyPassword("SafePassword123", pythonHash) {
		t.Fatal("Go must verify the final Python argon2-cffi hash format")
	}
	if VerifyPassword("wrong", pythonHash) {
		t.Fatal("wrong password accepted")
	}
	hash, err := HashPassword("SafePassword123")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword("SafePassword123", hash) {
		t.Fatal("Go hash did not round trip")
	}
}

func TestTokenHashStable(t *testing.T) {
	got := TokenHash("secret", "token")
	const want = "e941110e3d2bfe82621f0e3e1434730d7305d106c5f68c87165d0b27a4611a4a"
	if got != want {
		t.Fatalf("token hash changed: %s", got)
	}
}
