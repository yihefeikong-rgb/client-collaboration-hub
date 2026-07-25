package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestBindingResolverHashesFilesAndMarksMissingFilesUnavailable(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "reports"), 0o700); err != nil {
		t.Fatal(err)
	}
	content := []byte("test output\n")
	if err := os.WriteFile(filepath.Join(root, "reports", "test.txt"), content, 0o600); err != nil {
		t.Fatal(err)
	}
	resolver := NewFileBindingResolver()
	binding := ProjectBinding{DeviceID: "device-1", ProjectID: "project-1", LocalPath: root, BoundAt: journalTime}
	resolved, err := resolver.Resolve(context.Background(), binding, "reports/test.txt")
	wantHash := sha256.Sum256(content)
	if err != nil || !resolved.Available || resolved.Size != int64(len(content)) || resolved.SHA256 != hex.EncodeToString(wantHash[:]) {
		t.Fatalf("Resolve() = %+v, %v", resolved, err)
	}
	missing, err := resolver.Resolve(context.Background(), binding, "reports/missing.txt")
	if err != nil || missing.Available || missing.SHA256 != "" || missing.Size != 0 {
		t.Fatalf("missing Resolve() = %+v, %v", missing, err)
	}
}

func TestBindingResolverRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "outside-link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink is unavailable in this environment: %v", err)
	}
	resolver := NewFileBindingResolver()
	binding := ProjectBinding{DeviceID: "device-1", ProjectID: "project-1", LocalPath: root, BoundAt: journalTime}
	if _, err := resolver.Resolve(context.Background(), binding, "outside-link"); err == nil {
		t.Fatal("symlink escape was accepted")
	}
}
