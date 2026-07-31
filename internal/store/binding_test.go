package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/protocol"
)

func TestBindingStoreCreatesAndUpdatesDeviceLocalBinding(t *testing.T) {
	root := t.TempDir()
	registry := NewFileRegistryStore(root, FlockLocker{})
	if err := registry.CreateProject(context.Background(), protocol.Project{ID: "project-1", Name: "Demo", CreatedAt: journalTime}); err != nil {
		t.Fatal(err)
	}
	store := NewFileBindingStore(root, FlockLocker{}, registry)
	firstPath := filepath.Join(root, "first")
	secondPath := filepath.Join(root, "second")
	for _, path := range []string{firstPath, secondPath} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	first := ProjectBinding{DeviceID: "device-1", ProjectID: "project-1", LocalPath: firstPath, Revision: "r1", BoundAt: journalTime}
	if err := store.BindProject(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	expectedFirst, err := filepath.EvalSymlinks(firstPath)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := store.ReadBinding(context.Background(), first.DeviceID, first.ProjectID)
	if err != nil || stored.LocalPath != filepath.Clean(expectedFirst) || stored.Revision != "r1" || !store.BindingAvailable(context.Background(), first.DeviceID, first.ProjectID) {
		t.Fatalf("ReadBinding() = %+v, %v", stored, err)
	}
	updated := first
	updated.LocalPath = secondPath
	updated.Revision = "r2"
	if err := store.BindProject(context.Background(), updated); err != nil {
		t.Fatal(err)
	}
	expectedSecond, err := filepath.EvalSymlinks(secondPath)
	if err != nil {
		t.Fatal(err)
	}
	stored, err = store.ReadBinding(context.Background(), first.DeviceID, first.ProjectID)
	if err != nil || stored.LocalPath != filepath.Clean(expectedSecond) || stored.Revision != "r2" {
		t.Fatalf("updated binding = %+v, %v", stored, err)
	}
	if _, err := os.Stat(filepath.Join(root, "bindings", first.DeviceID, first.ProjectID+".local.json")); err != nil {
		t.Fatal(err)
	}
}

func TestBindingStoreListsProjectBindingsInDeviceOrder(t *testing.T) {
	root := t.TempDir()
	registry := NewFileRegistryStore(root, FlockLocker{})
	if err := registry.CreateProject(context.Background(), protocol.Project{ID: "project-1", Name: "Demo", CreatedAt: journalTime}); err != nil {
		t.Fatal(err)
	}
	store := NewFileBindingStore(root, FlockLocker{}, registry)
	for _, deviceID := range []string{"device-2", "device-1"} {
		path := filepath.Join(root, deviceID)
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := store.BindProject(context.Background(), ProjectBinding{DeviceID: deviceID, ProjectID: "project-1", LocalPath: path, BoundAt: journalTime}); err != nil {
			t.Fatal(err)
		}
	}
	bindings, err := store.ListBindings(context.Background(), "project-1")
	if err != nil || len(bindings) != 2 || bindings[0].DeviceID != "device-1" || bindings[1].DeviceID != "device-2" {
		t.Fatalf("ListBindings() = %#v, %v", bindings, err)
	}
}

func TestBindingStoreRejectsUnknownProjectAndUnavailablePath(t *testing.T) {
	root := t.TempDir()
	registry := NewFileRegistryStore(root, FlockLocker{})
	store := NewFileBindingStore(root, FlockLocker{}, registry)
	if err := store.BindProject(context.Background(), ProjectBinding{DeviceID: "device-1", ProjectID: "project-1", LocalPath: root, BoundAt: journalTime}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown project error = %v", err)
	}
	if _, err := store.ReadBinding(context.Background(), "device-1", "project-1"); !errors.Is(err, ErrBindingUnavailable) {
		t.Fatalf("missing binding error = %v", err)
	}
	if store.BindingAvailable(context.Background(), "device-1", "project-1") {
		t.Fatal("missing binding is available")
	}
}

func TestBindingStoreDoesNotPublishShortWrite(t *testing.T) {
	root := t.TempDir()
	registry := NewFileRegistryStore(root, FlockLocker{})
	if err := registry.CreateProject(context.Background(), protocol.Project{ID: "project-1", Name: "Demo", CreatedAt: journalTime}); err != nil {
		t.Fatal(err)
	}
	store := NewFileBindingStore(root, FlockLocker{}, registry)
	store.FS = &faultFS{partialTemp: true}
	if err := store.BindProject(context.Background(), ProjectBinding{DeviceID: "device-1", ProjectID: "project-1", LocalPath: root, BoundAt: journalTime}); err == nil {
		t.Fatal("short binding write accepted")
	}
	if _, err := os.Stat(filepath.Join(root, "bindings", "device-1", "project-1.local.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("published partial binding: %v", err)
	}
}
