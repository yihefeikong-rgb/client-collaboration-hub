//go:build windows

package cli

import (
	"errors"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestReasonixDesktopImageTrustAcceptsOnlyFixedProductionRoot(t *testing.T) {
	const installRoot = `C:\Program Files\Reasonix`
	verified := ""
	err := verifyReasonixDesktopImagePathWith(
		filepath.Join(installRoot, reasonixDesktopExecutable),
		[]string{installRoot},
		"",
		identityReasonixDesktopPath,
		func(path string) error {
			verified = path
			return nil
		},
	)
	if err != nil {
		t.Fatalf("trusted production image: %v", err)
	}
	if !sameReasonixDesktopPath(verified, filepath.Join(installRoot, reasonixDesktopExecutable)) {
		t.Fatalf("signature verified %q, want %q", verified, filepath.Join(installRoot, reasonixDesktopExecutable))
	}

	for _, imagePath := range []string{
		`C:\Temp\reasonix-desktop.exe`,
		`C:\Program Files\Reasonix\nested\reasonix-desktop.exe`,
		`C:\Program Files\Reasonix\reasonix-cli.exe`,
		`reasonix-desktop.exe`,
	} {
		if err := verifyReasonixDesktopImagePathWith(imagePath, []string{installRoot}, "", identityReasonixDesktopPath, func(string) error { return nil }); err == nil {
			t.Fatalf("untrusted image %q was accepted", imagePath)
		}
	}
}

func TestReasonixDesktopImageTrustRequiresAuthenticodeForProduction(t *testing.T) {
	err := verifyReasonixDesktopImagePathWith(
		`C:\Program Files\Reasonix\reasonix-desktop.exe`,
		[]string{`C:\Program Files\Reasonix`},
		"",
		identityReasonixDesktopPath,
		func(string) error { return errors.New("unsigned") },
	)
	if err == nil {
		t.Fatal("unsigned production image was accepted")
	}
}

func TestReasonixDesktopImageTrustAllowsExplicitSourceBuildOnly(t *testing.T) {
	const developmentRoot = `D:\src\DeepSeek-Reasonix`
	imagePath := filepath.Join(developmentRoot, "desktop", "build", "bin", reasonixDesktopExecutable)
	err := verifyReasonixDesktopImagePathWith(imagePath, nil, developmentRoot, identityReasonixDesktopPath, nil)
	if err != nil {
		t.Fatalf("explicit source build: %v", err)
	}

	for _, root := range []string{`C:\Temp\DeepSeek-Reasonix`, developmentRoot} {
		candidate := filepath.Join(root, reasonixDesktopExecutable)
		if root == developmentRoot {
			candidate = filepath.Join(root, "desktop", "build", reasonixDesktopExecutable)
		}
		if err := verifyReasonixDesktopImagePathWith(candidate, nil, root, identityReasonixDesktopPath, nil); err == nil {
			t.Fatalf("invalid development image %q was accepted", candidate)
		}
	}
}

func TestReasonixDesktopDevelopmentRootRequiresNonTemporarySourceLayout(t *testing.T) {
	if got := reasonixDesktopDevelopmentRootFrom(`D:\src\DeepSeek-Reasonix`, identityReasonixDesktopPath, func(string) bool { return true }); got != `D:\src\DeepSeek-Reasonix` {
		t.Fatalf("development root = %q", got)
	}
	for _, root := range []string{`C:\Temp\DeepSeek-Reasonix`, `DeepSeek-Reasonix`} {
		if got := reasonixDesktopDevelopmentRootFrom(root, identityReasonixDesktopPath, func(string) bool { return true }); got != "" {
			t.Fatalf("development root %q = %q, want rejected", root, got)
		}
	}
	if got := reasonixDesktopDevelopmentRootFrom(`D:\src\DeepSeek-Reasonix`, identityReasonixDesktopPath, func(string) bool { return false }); got != "" {
		t.Fatalf("non-source root = %q, want rejected", got)
	}
}

func TestReasonixDesktopTrustedInstallRootsUseKnownFolders(t *testing.T) {
	roots := reasonixDesktopTrustedInstallRootsFrom(func(folderID *windows.KNOWNFOLDERID) (string, error) {
		switch folderID {
		case windows.FOLDERID_LocalAppData:
			return `C:\Users\alice\AppData\Local`, nil
		case windows.FOLDERID_ProgramFiles:
			return `C:\Program Files`, nil
		default:
			return "", errors.New("unexpected known folder")
		}
	})
	want := []string{
		`C:\Users\alice\AppData\Local\Programs\Reasonix`,
		`C:\Program Files\Reasonix`,
	}
	if len(roots) != len(want) {
		t.Fatalf("trusted roots = %v, want %v", roots, want)
	}
	for i := range want {
		if !sameReasonixDesktopPath(roots[i], want[i]) {
			t.Fatalf("trusted roots = %v, want %v", roots, want)
		}
	}
}

func identityReasonixDesktopPath(path string) (string, error) {
	return path, nil
}
