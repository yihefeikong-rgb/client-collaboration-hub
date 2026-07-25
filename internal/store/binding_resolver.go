package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/protocol"
)

type ResolvedFileRef struct {
	RelativeRef string `json:"relative_ref"`
	Size        int64  `json:"size,omitempty"`
	SHA256      string `json:"sha256,omitempty"`
	Available   bool   `json:"available"`
}

type BindingResolver interface {
	Resolve(context.Context, ProjectBinding, string) (ResolvedFileRef, error)
}

type FileBindingResolver struct{}

func NewFileBindingResolver() *FileBindingResolver { return &FileBindingResolver{} }

func (*FileBindingResolver) Resolve(ctx context.Context, binding ProjectBinding, ref string) (ResolvedFileRef, error) {
	result := ResolvedFileRef{RelativeRef: ref}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := binding.validate(binding.DeviceID, binding.ProjectID); err != nil {
		return result, err
	}
	if err := protocol.ValidatePortableFileRef(ref); err != nil {
		return result, err
	}
	root, err := filepath.EvalSymlinks(binding.LocalPath)
	if errors.Is(err, os.ErrNotExist) {
		return result, fmt.Errorf("%w: binding root is unavailable", ErrBindingUnavailable)
	}
	if err != nil {
		return result, err
	}
	root = filepath.Clean(root)
	rootInfo, err := os.Stat(root)
	if err != nil {
		return result, err
	}
	if !rootInfo.IsDir() {
		return result, fmt.Errorf("%w: binding root is not a directory", ErrBindingUnavailable)
	}
	candidate := filepath.Clean(filepath.Join(root, filepath.FromSlash(ref)))
	if !pathWithinRoot(root, candidate) {
		return result, fmt.Errorf("evidence file escapes binding root")
	}
	resolved, err := filepath.EvalSymlinks(candidate)
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	resolved = filepath.Clean(resolved)
	if !pathWithinRoot(root, resolved) {
		return result, fmt.Errorf("evidence file escapes binding root")
	}
	info, err := os.Stat(resolved)
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	if !info.Mode().IsRegular() {
		return result, nil
	}
	file, err := os.Open(resolved)
	if err != nil {
		return result, err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return result, err
	}
	result.Size = info.Size()
	result.SHA256 = hex.EncodeToString(hash.Sum(nil))
	result.Available = true
	return result, nil
}

func pathWithinRoot(root, candidate string) bool {
	root = filepath.Clean(root)
	candidate = filepath.Clean(candidate)
	if runtime.GOOS == "windows" {
		root = strings.ToLower(root)
		candidate = strings.ToLower(candidate)
	}
	relative, err := filepath.Rel(root, candidate)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return false
	}
	return true
}
