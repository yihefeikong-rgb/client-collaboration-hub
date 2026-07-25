package protocol

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

var (
	credentialValuePattern = regexp.MustCompile(`(?i)\b(?:secret|token|password|credential|api[_-]?key)\s*(?:=|:)\s*\S+|\bghp_[A-Za-z0-9]+\b|\bgithub_pat_[A-Za-z0-9_]+\b|\bbearer\s+[A-Za-z0-9._~+/=-]{8,}\b`)
	urlSchemePattern       = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*:`)
)

// ValidatePortableText rejects values that would expose device-local state or
// obvious credentials when persisted in transferable collaboration data.
func ValidatePortableText(field, value string) error {
	if containsControlCharacter(value) || credentialValuePattern.MatchString(value) || containsLocalFilesystemPath(value) {
		return fmt.Errorf("%s contains unsafe portable content", field)
	}
	return nil
}

// ValidatePortableFileRef restricts Evidence FileRefs to canonical project
// relative paths that use slash separators on every platform.
func ValidatePortableFileRef(ref string) error {
	if strings.TrimSpace(ref) == "" || strings.TrimSpace(ref) != ref {
		return fmt.Errorf("evidence file_ref is invalid")
	}
	if err := ValidatePortableText("evidence file_ref", ref); err != nil {
		return err
	}
	if strings.Contains(ref, `\`) || strings.HasPrefix(ref, "/") || urlSchemePattern.MatchString(ref) {
		return fmt.Errorf("evidence file_ref is invalid")
	}
	for _, segment := range strings.Split(ref, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return fmt.Errorf("evidence file_ref is invalid")
		}
	}
	return nil
}

func containsControlCharacter(value string) bool {
	for _, char := range value {
		if unicode.IsControl(char) {
			return true
		}
	}
	return false
}

func containsLocalFilesystemPath(value string) bool {
	if strings.Contains(value, `\\`) || strings.Contains(strings.ToLower(value), "file://") {
		return true
	}
	for index := 0; index+2 < len(value); index++ {
		if isASCIIAlpha(value[index]) && value[index+1] == ':' && (value[index+2] == '/' || value[index+2] == '\\') {
			return true
		}
	}
	for index := 0; index < len(value); index++ {
		if index > 0 && !pathBoundary(value[index-1]) {
			continue
		}
		for _, prefix := range []string{"/home/", "/Users/", "/root/", "/tmp/", "/var/", "/etc/", "/opt/", "/usr/", "/mnt/", "/srv/", "/workspace/", "/private/", "/Volumes/", "/run/", "/bin/", "/sbin/", "/lib/"} {
			if strings.HasPrefix(value[index:], prefix) {
				return true
			}
		}
	}
	return false
}

func pathBoundary(value byte) bool {
	return isASCIIWhitespace(value) || strings.ContainsRune(`"'([{:;=`, rune(value))
}

func isASCIIWhitespace(value byte) bool {
	return value == ' ' || value == '\t' || value == '\r' || value == '\n'
}

func isASCIIAlpha(value byte) bool {
	return (value >= 'A' && value <= 'Z') || (value >= 'a' && value <= 'z')
}
