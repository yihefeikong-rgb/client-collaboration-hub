package handoff

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

var (
	ErrHandoffAlreadyExists  = errors.New("handoff output already exists")
	ErrHandoffOutcomeUnknown = errors.New("handoff publish outcome unknown")
	ErrHandoffUnsafeOutput   = errors.New("handoff output is unsafe")
)

type DirectoryPublisher struct {
	WorkspaceRoot string
	Verify        func(string) error
}

func NewDirectoryPublisher(workspaceRoot string) DirectoryPublisher {
	return DirectoryPublisher{WorkspaceRoot: workspaceRoot}
}

func (p DirectoryPublisher) Publish(outputDir string, packageData DeliveryPackage) error {
	outputDir, err := filepath.Abs(outputDir)
	if err != nil {
		return err
	}
	outputDir = filepath.Clean(outputDir)
	if err := p.validateWorkspaceOutput(outputDir); err != nil {
		return err
	}
	if err := rejectExistingOutput(outputDir); err != nil {
		return err
	}
	parent, base := filepath.Dir(outputDir), filepath.Base(outputDir)
	if base == "." || base == string(filepath.Separator) {
		return fmt.Errorf("%w: invalid output path", ErrHandoffUnsafeOutput)
	}
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	if err := p.validateWorkspaceOutput(outputDir); err != nil {
		return err
	}
	if err := rejectExistingOutput(outputDir); err != nil {
		return err
	}
	temporary, err := os.MkdirTemp(parent, "."+base+".tmp-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temporary)
	files := []struct {
		name string
		data []byte
	}{
		{"handoff.md", packageData.Handoff},
		{"manifest.json", packageData.Manifest},
		{"candidate-response.json", packageData.CandidateResponse},
		{"candidate-response.schema.json", packageData.CandidateResponseSchema},
	}
	for _, file := range files {
		if err := writePackageFile(filepath.Join(temporary, file.name), file.data); err != nil {
			return err
		}
	}
	if err := os.Mkdir(outputDir, 0o700); err != nil {
		if existingErr := rejectExistingOutput(outputDir); existingErr != nil {
			return existingErr
		}
		return err
	}
	for _, file := range files {
		if err := copyPackageFile(filepath.Join(temporary, file.name), filepath.Join(outputDir, file.name)); err != nil {
			return fmt.Errorf("%w: %v", ErrHandoffOutcomeUnknown, err)
		}
	}
	if err := p.verify(outputDir); err != nil {
		return fmt.Errorf("%w: %v", ErrHandoffOutcomeUnknown, err)
	}
	return nil
}

func rejectExistingOutput(outputDir string) error {
	info, err := os.Lstat(outputDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: output is an existing symlink", ErrHandoffUnsafeOutput)
	}
	return fmt.Errorf("%w: %s", ErrHandoffAlreadyExists, outputDir)
}

func (p DirectoryPublisher) validateWorkspaceOutput(outputDir string) error {
	if p.WorkspaceRoot == "" {
		return nil
	}
	root, err := filepath.Abs(p.WorkspaceRoot)
	if err != nil {
		return err
	}
	root = filepath.Clean(root)
	if samePath(root, outputDir) {
		return fmt.Errorf("%w: repository root", ErrHandoffUnsafeOutput)
	}
	collaboration := filepath.Join(root, "collaboration")
	if samePath(collaboration, outputDir) || pathWithin(collaboration, outputDir) {
		return fmt.Errorf("%w: collaboration data", ErrHandoffUnsafeOutput)
	}
	parent := filepath.Dir(outputDir)
	resolvedParent, err := filepath.EvalSymlinks(parent)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	resolvedOutput := filepath.Join(resolvedParent, filepath.Base(outputDir))
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	resolvedCollaboration := filepath.Join(resolvedRoot, "collaboration")
	if samePath(resolvedRoot, resolvedOutput) || samePath(resolvedCollaboration, resolvedOutput) || pathWithin(resolvedCollaboration, resolvedOutput) {
		return fmt.Errorf("%w: resolved collaboration data", ErrHandoffUnsafeOutput)
	}
	return nil
}

func (p DirectoryPublisher) verify(outputDir string) error {
	if p.Verify != nil {
		return p.Verify(outputDir)
	}
	_, err := VerifyPackage(outputDir)
	return err
}

func VerifyPackage(packageDir string) (Manifest, error) {
	info, err := os.Lstat(packageDir)
	if err != nil {
		return Manifest{}, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return Manifest{}, fmt.Errorf("handoff package is not a real directory")
	}
	entries, err := os.ReadDir(packageDir)
	if err != nil {
		return Manifest{}, err
	}
	expected := map[string]bool{
		"handoff.md":                     false,
		"manifest.json":                  false,
		"candidate-response.json":        false,
		"candidate-response.schema.json": false,
	}
	if len(entries) != len(expected) {
		return Manifest{}, fmt.Errorf("handoff package contains unexpected files")
	}
	for _, entry := range entries {
		if _, ok := expected[entry.Name()]; !ok || entry.Type()&os.ModeSymlink != 0 || entry.IsDir() {
			return Manifest{}, fmt.Errorf("handoff package contains unsafe file")
		}
		entryInfo, err := entry.Info()
		if err != nil || !entryInfo.Mode().IsRegular() {
			return Manifest{}, fmt.Errorf("handoff package contains unsafe file")
		}
		expected[entry.Name()] = true
	}
	for name, present := range expected {
		if !present {
			return Manifest{}, fmt.Errorf("handoff package is missing %s", name)
		}
	}
	manifestData, err := os.ReadFile(filepath.Join(packageDir, "manifest.json"))
	if err != nil {
		return Manifest{}, err
	}
	manifest, err := DecodeManifest(manifestData)
	if err != nil {
		return Manifest{}, err
	}
	handoffData, err := os.ReadFile(filepath.Join(packageDir, "handoff.md"))
	if err != nil {
		return Manifest{}, fmt.Errorf("handoff package has invalid handoff.md")
	}
	expectedHandoff, err := renderHandoff(manifest)
	if err != nil || !bytes.Equal(handoffData, expectedHandoff) {
		return Manifest{}, fmt.Errorf("handoff package has invalid handoff.md")
	}
	schema, err := os.ReadFile(filepath.Join(packageDir, "candidate-response.schema.json"))
	if err != nil || !bytes.Equal(schema, CandidateResponseSchema()) {
		return Manifest{}, fmt.Errorf("handoff package has invalid candidate response schema")
	}
	responseData, err := os.ReadFile(filepath.Join(packageDir, "candidate-response.json"))
	if err != nil {
		return Manifest{}, err
	}
	response, err := DecodeCandidateResponse(responseData)
	if err != nil {
		return Manifest{}, err
	}
	if err := ValidateCandidateTemplate(manifest, response); err != nil {
		return Manifest{}, err
	}
	expectedTemplate, err := marshalCandidateResponse(NewCandidateResponse(manifest))
	if err != nil || !bytes.Equal(responseData, expectedTemplate) {
		return Manifest{}, fmt.Errorf("handoff package has invalid candidate response template")
	}
	return manifest, nil
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

func copyPackageFile(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	if copyErr == nil {
		copyErr = output.Sync()
	}
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func samePath(first, second string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Clean(first), filepath.Clean(second))
	}
	return filepath.Clean(first) == filepath.Clean(second)
}

func pathWithin(root, candidate string) bool {
	root = filepath.Clean(root)
	candidate = filepath.Clean(candidate)
	if runtime.GOOS == "windows" {
		root = strings.ToLower(root)
		candidate = strings.ToLower(candidate)
	}
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}
