package services

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanStorageTotal(t *testing.T) {
	root := t.TempDir()
	// Two files totalling 9 bytes across a nested directory.
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "b.txt"), []byte("abcd"), 0o600); err != nil {
		t.Fatal(err)
	}

	if got, _ := scanStorage(root); got != 9 {
		t.Errorf("scanStorage total = %d, want 9", got)
	}
}

func TestScanStorageMissingPathIsZero(t *testing.T) {
	if got, _ := scanStorage(filepath.Join(t.TempDir(), "does-not-exist")); got != 0 {
		t.Errorf("scanStorage total of missing path = %d, want 0", got)
	}
}
