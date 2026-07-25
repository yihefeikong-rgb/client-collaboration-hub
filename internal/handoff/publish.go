package handoff

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

type DirectoryPublisher struct{}

func (DirectoryPublisher) Publish(outputDir string, packageData DeliveryPackage, force bool) error {
	outputDir, err := filepath.Abs(outputDir)
	if err != nil {
		return err
	}
	outputDir = filepath.Clean(outputDir)
	parent, base := filepath.Dir(outputDir), filepath.Base(outputDir)
	if base == "." || base == string(filepath.Separator) {
		return fmt.Errorf("handoff output path is invalid")
	}
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(outputDir)
	exists := err == nil
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if exists && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
		return fmt.Errorf("handoff output must be a directory")
	}
	if exists && !force {
		return fmt.Errorf("handoff output already exists")
	}
	temporary, err := os.MkdirTemp(parent, "."+base+".tmp-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temporary)
	if err := writePackageFile(filepath.Join(temporary, "handoff.md"), packageData.Handoff); err != nil {
		return err
	}
	if err := writePackageFile(filepath.Join(temporary, "manifest.json"), packageData.Manifest); err != nil {
		return err
	}
	if !exists {
		return os.Rename(temporary, outputDir)
	}
	backup := filepath.Join(parent, fmt.Sprintf(".%s.previous-%d", base, time.Now().UnixNano()))
	if err := os.Rename(outputDir, backup); err != nil {
		return err
	}
	if err := os.Rename(temporary, outputDir); err != nil {
		if restoreErr := os.Rename(backup, outputDir); restoreErr != nil {
			return fmt.Errorf("publish failed and previous package could not be restored")
		}
		return err
	}
	return os.RemoveAll(backup)
}

func writePackageFile(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	writeErr := error(nil)
	if count, err := file.Write(data); err != nil {
		writeErr = err
	} else if count != len(data) {
		writeErr = io.ErrShortWrite
	}
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}
