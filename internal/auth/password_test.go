package auth

import (
	"bytes"
	"strings"
	"testing"
)

func testHasher() PasswordHasher {
	return PasswordHasher{
		Params: Argon2Params{MemoryKiB: 64, Time: 1, Threads: 1, SaltBytes: 16, KeyBytes: 32},
		Random: bytes.NewReader(bytes.Repeat([]byte{0x5a}, 4096)),
	}
}

func TestPasswordHashAndVerify(t *testing.T) {
	t.Parallel()

	hasher := testHasher()
	encoded, err := hasher.Hash("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(encoded, "correct horse") {
		t.Fatal("encoded password contains plaintext")
	}
	match, err := hasher.Verify(encoded, "correct horse battery staple")
	if err != nil || !match {
		t.Fatalf("Verify() = %v, %v; want true, nil", match, err)
	}
	match, err = hasher.Verify(encoded, "wrong password")
	if err != nil {
		t.Fatal(err)
	}
	if match {
		t.Fatal("Verify() accepted wrong password")
	}
}

func TestPasswordHashRejectsWeakAndHostileInputs(t *testing.T) {
	t.Parallel()

	hasher := testHasher()
	if _, err := hasher.Hash("short"); err == nil {
		t.Fatal("Hash() accepted a short password")
	}
	oversized := "$argon2id$v=19$m=1048577,t=1,p=1$WlpaWlpaWlpaWlpaWlpaWg$WlpaWlpaWlpaWlpaWlpaWlpaWlpaWlpaWlpaWlpaWlo"
	if _, err := hasher.Verify(oversized, "correct horse battery staple"); err == nil {
		t.Fatal("Verify() accepted hostile Argon2 memory parameters")
	}
}
