package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
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

func TestBindingResolverRejectsFilesOverConfiguredHashLimit(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "large.txt"), []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	resolver := NewFileBindingResolver(BindingResolverConfig{MaxHashFileSize: 4})
	binding := ProjectBinding{DeviceID: "device-1", ProjectID: "project-1", LocalPath: root, BoundAt: journalTime}
	if _, err := resolver.Resolve(context.Background(), binding, "large.txt"); !errors.Is(err, ErrHashFileTooLarge) {
		t.Fatalf("large file error = %v", err)
	}
}

func TestBindingResolverChecksCancellationAndConcurrentChanges(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "report.txt")
	if err := os.WriteFile(path, []byte("initial"), 0o600); err != nil {
		t.Fatal(err)
	}
	binding := ProjectBinding{DeviceID: "device-1", ProjectID: "project-1", LocalPath: root, BoundAt: journalTime}
	ctx, cancel := context.WithCancel(context.Background())
	resolver := NewFileBindingResolver()
	resolver.beforeRead = cancel
	if _, err := resolver.Resolve(ctx, binding, "report.txt"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled hash error = %v", err)
	}
	resolver = NewFileBindingResolver()
	resolver.beforeRead = func() {
		if err := os.WriteFile(path, []byte("changed content"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := resolver.Resolve(context.Background(), binding, "report.txt"); !errors.Is(err, ErrFileChangedDuringHash) {
		t.Fatalf("changed file error = %v", err)
	}
}
