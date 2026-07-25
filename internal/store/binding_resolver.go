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

const DefaultMaxHashFileSize int64 = 64 << 20

var (
	ErrHashFileTooLarge      = errors.New("evidence file exceeds hash size limit")
	ErrFileChangedDuringHash = errors.New("evidence file changed during hashing")
)

type BindingResolverConfig struct {
	MaxHashFileSize int64
}

type FileBindingResolver struct {
	MaxHashFileSize int64
	beforeRead      func()
}

func NewFileBindingResolver(config ...BindingResolverConfig) *FileBindingResolver {
	maxHashFileSize := DefaultMaxHashFileSize
	if len(config) > 0 && config[0].MaxHashFileSize > 0 {
		maxHashFileSize = config[0].MaxHashFileSize
	}
	return &FileBindingResolver{MaxHashFileSize: maxHashFileSize}
}

func (r *FileBindingResolver) Resolve(ctx context.Context, binding ProjectBinding, ref string) (ResolvedFileRef, error) {
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
	file, err := os.Open(resolved)
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil {
		return result, err
	}
	if !before.Mode().IsRegular() {
		return result, nil
	}
	if before.Size() > r.maxHashFileSize() {
		return result, fmt.Errorf("%w: %s", ErrHashFileTooLarge, ref)
	}
	if r.beforeRead != nil {
		r.beforeRead()
	}
	hash := sha256.New()
	buffer := make([]byte, 64*1024)
	for {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		count, readErr := file.Read(buffer)
		if count > 0 {
			if _, err := hash.Write(buffer[:count]); err != nil {
				return result, err
			}
		}
		if readErr == nil {
			continue
		}
		if readErr == io.EOF {
			break
		}
		return result, readErr
	}
	after, err := file.Stat()
	if err != nil {
		return result, err
	}
	current, err := os.Stat(resolved)
	if errors.Is(err, os.ErrNotExist) {
		return result, ErrFileChangedDuringHash
	}
	if err != nil {
		return result, err
	}
	if !os.SameFile(before, after) || !os.SameFile(before, current) || before.Size() != after.Size() || before.Size() != current.Size() || !before.ModTime().Equal(after.ModTime()) || !before.ModTime().Equal(current.ModTime()) {
		return result, ErrFileChangedDuringHash
	}
	result.Size = before.Size()
	result.SHA256 = hex.EncodeToString(hash.Sum(nil))
	result.Available = true
	return result, nil
}

func (r *FileBindingResolver) maxHashFileSize() int64 {
	if r == nil || r.MaxHashFileSize <= 0 {
		return DefaultMaxHashFileSize
	}
	return r.MaxHashFileSize
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
