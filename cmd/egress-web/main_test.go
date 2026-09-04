package main

import (
	"strings"
	"testing"
)

func TestReadPasswordAcceptsOneLineWithoutAlteringSpaces(t *testing.T) {
	t.Parallel()

	password, err := readPassword(strings.NewReader("  long password with spaces  \r\n"))
	if err != nil {
		t.Fatal(err)
	}
	if password != "  long password with spaces  " {
		t.Fatalf("password was altered")
	}
}

func TestReadPasswordRejectsMultipleLinesAndOversize(t *testing.T) {
	t.Parallel()

	if _, err := readPassword(strings.NewReader("first\nsecond\n")); err == nil {
		t.Fatal("readPassword() accepted multiple lines")
	}
	if _, err := readPassword(strings.NewReader(strings.Repeat("x", 1025) + "\n")); err == nil {
		t.Fatal("readPassword() accepted more than 1024 bytes")
	}
}
