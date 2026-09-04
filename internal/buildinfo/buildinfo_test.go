package buildinfo

import "testing"

func TestDevelopmentMetadataIsPresent(t *testing.T) {
	if Version == "" || Commit == "" || Date == "" {
		t.Fatal("build metadata must never be empty")
	}
}
