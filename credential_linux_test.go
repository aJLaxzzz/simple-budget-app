package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLinuxCredentialFileGuards(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "credential")
	if err := os.WriteFile(p, []byte("Test-pass-12\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if value, err := readSystemdPasswordFile(p); err != nil || value != "Test-pass-12" {
		t.Fatal("private credential refused", err)
	}
	if err := os.Chmod(p, 0440); err != nil {
		t.Fatal(err)
	}
	if _, err := readSystemdPasswordFile(p); err == nil {
		t.Fatal("ordinary group-readable credential accepted without ACL")
	}
	if err := os.Chmod(p, 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(p, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readSystemdPasswordFile(link); err == nil {
		t.Fatal("symlink accepted")
	}
	if err := os.Link(p, filepath.Join(dir, "hard")); err != nil {
		t.Fatal(err)
	}
	if _, err := readSystemdPasswordFile(p); err == nil {
		t.Fatal("hardlink accepted")
	}
}
