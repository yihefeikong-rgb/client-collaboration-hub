//go:build windows

package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	reasonixDesktopExecutable         = "reasonix-desktop.exe"
	reasonixDesktopDevelopmentRootEnv = "COLLAB_REASONIX_DESKTOP_DEV_ROOT"
)

type reasonixDesktopPathResolver func(string) (string, error)

type reasonixDesktopSignatureVerifier func(string) error

// verifyReasonixDesktopProcess binds bridge discovery to the installed desktop
// process rather than trusting a PID and loopback endpoint written by another
// local process.
func verifyReasonixDesktopProcess(pid int) error {
	if pid <= 0 || uint64(pid) > uint64(^uint32(0)) {
		return errors.New("bridge PID is invalid")
	}
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return fmt.Errorf("open bridge PID: %w", err)
	}
	defer windows.CloseHandle(process)

	pathBuffer := make([]uint16, 32768)
	pathLength := uint32(len(pathBuffer))
	if err := windows.QueryFullProcessImageName(process, 0, &pathBuffer[0], &pathLength); err != nil {
		return fmt.Errorf("read bridge process image: %w", err)
	}
	imagePath := windows.UTF16ToString(pathBuffer[:pathLength])
	if err := verifyReasonixDesktopImagePath(imagePath); err != nil {
		return incompatibleReasonixDesktopBridge(err.Error())
	}
	return nil
}

// verifyReasonixDesktopImagePath accepts only a signed desktop executable in a
// fixed Windows install root, or an explicitly selected local source build.
// Discovery data supplies only the PID; it never supplies a trusted root.
func verifyReasonixDesktopImagePath(imagePath string) error {
	return verifyReasonixDesktopImagePathWith(
		imagePath,
		reasonixDesktopTrustedInstallRoots(),
		reasonixDesktopDevelopmentRoot(),
		filepath.EvalSymlinks,
		verifyReasonixDesktopAuthenticode,
	)
}

func verifyReasonixDesktopImagePathWith(imagePath string, installRoots []string, developmentRoot string, resolvePath reasonixDesktopPathResolver, verifySignature reasonixDesktopSignatureVerifier) error {
	cleanImagePath, ok := cleanReasonixDesktopAbsolutePath(imagePath)
	if !ok {
		return errors.New("desktop executable path is invalid")
	}
	if isReasonixDesktopTemporaryPath(cleanImagePath) {
		return errors.New("desktop executable path is under a temporary directory")
	}
	canonicalImagePath, err := resolveReasonixDesktopPath(cleanImagePath, resolvePath)
	if err != nil {
		return errors.New("desktop executable path cannot be resolved")
	}
	if isReasonixDesktopTemporaryPath(canonicalImagePath) {
		return errors.New("desktop executable path is under a temporary directory")
	}

	for _, root := range installRoots {
		canonicalRoot, err := resolveReasonixDesktopPath(root, resolvePath)
		if err != nil {
			continue
		}
		if !sameReasonixDesktopPath(canonicalImagePath, filepath.Join(canonicalRoot, reasonixDesktopExecutable)) {
			continue
		}
		if verifySignature == nil || verifySignature(canonicalImagePath) != nil {
			return errors.New("desktop executable Authenticode signature is not trusted")
		}
		return nil
	}

	if developmentRoot != "" {
		canonicalDevelopmentRoot, err := resolveReasonixDesktopPath(developmentRoot, resolvePath)
		if err == nil && !isReasonixDesktopTemporaryPath(canonicalDevelopmentRoot) && sameReasonixDesktopPath(canonicalImagePath, filepath.Join(canonicalDevelopmentRoot, "desktop", "build", "bin", reasonixDesktopExecutable)) {
			return nil
		}
	}
	return errors.New("desktop executable is not in a trusted Reasonix installation root")
}

func reasonixDesktopTrustedInstallRoots() []string {
	return reasonixDesktopTrustedInstallRootsFrom(func(folderID *windows.KNOWNFOLDERID) (string, error) {
		return windows.KnownFolderPath(folderID, windows.KF_FLAG_DEFAULT)
	})
}

func reasonixDesktopTrustedInstallRootsFrom(knownFolderPath func(*windows.KNOWNFOLDERID) (string, error)) []string {
	if knownFolderPath == nil {
		return nil
	}
	roots := make([]string, 0, 2)
	if localAppData, err := knownFolderPath(windows.FOLDERID_LocalAppData); err == nil && strings.TrimSpace(localAppData) != "" {
		roots = append(roots, filepath.Join(localAppData, "Programs", "Reasonix"))
	}
	if programFiles, err := knownFolderPath(windows.FOLDERID_ProgramFiles); err == nil && strings.TrimSpace(programFiles) != "" {
		roots = append(roots, filepath.Join(programFiles, "Reasonix"))
	}
	return roots
}

// reasonixDesktopDevelopmentRoot is the only development trust extension. It
// reads the Hub process environment directly and never consults Reasonix state
// files or discovery JSON.
func reasonixDesktopDevelopmentRoot() string {
	return reasonixDesktopDevelopmentRootFrom(os.Getenv(reasonixDesktopDevelopmentRootEnv), filepath.EvalSymlinks, reasonixDesktopHasSourceLayout)
}

func reasonixDesktopDevelopmentRootFrom(root string, resolvePath reasonixDesktopPathResolver, hasSourceLayout func(string) bool) string {
	canonicalRoot, err := resolveReasonixDesktopPath(root, resolvePath)
	if err != nil || isReasonixDesktopTemporaryPath(canonicalRoot) || hasSourceLayout == nil || !hasSourceLayout(canonicalRoot) {
		return ""
	}
	return canonicalRoot
}

func reasonixDesktopHasSourceLayout(root string) bool {
	return reasonixDesktopRegularFile(filepath.Join(root, "go.mod")) &&
		reasonixDesktopRegularFile(filepath.Join(root, "desktop", "go.mod"))
}

func reasonixDesktopRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func resolveReasonixDesktopPath(path string, resolvePath reasonixDesktopPathResolver) (string, error) {
	clean, ok := cleanReasonixDesktopAbsolutePath(path)
	if !ok || resolvePath == nil {
		return "", errors.New("path is invalid")
	}
	resolved, err := resolvePath(clean)
	if err != nil {
		return "", err
	}
	clean, ok = cleanReasonixDesktopAbsolutePath(resolved)
	if !ok {
		return "", errors.New("resolved path is invalid")
	}
	return clean, nil
}

func cleanReasonixDesktopAbsolutePath(path string) (string, bool) {
	clean := filepath.Clean(strings.TrimSpace(path))
	return clean, filepath.IsAbs(clean)
}

func sameReasonixDesktopPath(left, right string) bool {
	return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
}

func isReasonixDesktopTemporaryPath(path string) bool {
	return reasonixDesktopPathWithin(path, `C:\Temp`) || reasonixDesktopPathWithin(path, os.TempDir())
}

func reasonixDesktopPathWithin(path, root string) bool {
	cleanPath, pathOK := cleanReasonixDesktopAbsolutePath(path)
	cleanRoot, rootOK := cleanReasonixDesktopAbsolutePath(root)
	if !pathOK || !rootOK {
		return false
	}
	relativePath, err := filepath.Rel(cleanRoot, cleanPath)
	if err != nil {
		return false
	}
	return relativePath != ".." && !strings.HasPrefix(relativePath, ".."+string(filepath.Separator)) && !filepath.IsAbs(relativePath)
}

func verifyReasonixDesktopAuthenticode(imagePath string) error {
	path16, err := windows.UTF16PtrFromString(imagePath)
	if err != nil {
		return err
	}
	trustFile := &windows.WinTrustFileInfo{
		Size:     uint32(unsafe.Sizeof(windows.WinTrustFileInfo{})),
		FilePath: path16,
	}
	trustData := &windows.WinTrustData{
		Size:                            uint32(unsafe.Sizeof(windows.WinTrustData{})),
		UIChoice:                        windows.WTD_UI_NONE,
		RevocationChecks:                windows.WTD_REVOKE_NONE,
		UnionChoice:                     windows.WTD_CHOICE_FILE,
		FileOrCatalogOrBlobOrSgnrOrCert: unsafe.Pointer(trustFile),
		StateAction:                     windows.WTD_STATEACTION_VERIFY,
		UIContext:                       windows.WTD_UICONTEXT_EXECUTE,
	}
	verifyErr := windows.WinVerifyTrustEx(
		windows.InvalidHWND,
		&windows.WINTRUST_ACTION_GENERIC_VERIFY_V2,
		trustData,
	)
	trustData.StateAction = windows.WTD_STATEACTION_CLOSE
	closeErr := windows.WinVerifyTrustEx(
		windows.InvalidHWND,
		&windows.WINTRUST_ACTION_GENERIC_VERIFY_V2,
		trustData,
	)
	if verifyErr != nil {
		if closeErr != nil {
			return fmt.Errorf("Authenticode verification failed: %v (trust state close: %w)", verifyErr, closeErr)
		}
		return fmt.Errorf("Authenticode verification failed: %w", verifyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("Authenticode trust state close: %w", closeErr)
	}
	return nil
}
