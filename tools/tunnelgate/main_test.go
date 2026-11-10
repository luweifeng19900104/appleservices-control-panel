package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLoadOrCreateKeyPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gate.key")
	first, err := loadOrCreateKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != accessKeyBytes*2 {
		t.Fatalf("key length = %d", len(first))
	}
	second, err := loadOrCreateKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if second != first {
		t.Fatal("existing access key was rotated")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0600 {
			t.Fatalf("key mode = %v", info.Mode().Perm())
		}
	}
}

func TestLoadOrCreateKeyRejectsInvalidFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gate.key")
	if err := os.WriteFile(path, []byte(strings.Repeat("z", accessKeyBytes*2)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadOrCreateKey(path); err == nil {
		t.Fatal("invalid access key was accepted")
	}
}
