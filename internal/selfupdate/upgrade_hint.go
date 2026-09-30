package selfupdate

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"

	"github.com/BurntSushi/toml"
)

// A package manager that owns the install can turn off self-update with a marker
// file or an instructions file; SHELF_SELF_UPDATE_AVAILABLE overrides both. The
// paths and names mirror mise's packaging contract so packagers can reuse it.
var (
	// disableMarkerPaths are marker files, relative to the install prefix, whose
	// presence disables self-update.
	disableMarkerPaths = []string{
		"lib/.disable-self-update",
		"lib/shelf/.disable-self-update",
		"lib64/shelf/.disable-self-update",
	}
	// instructionsPaths are update-instruction files, relative to the install
	// prefix, whose presence disables self-update and is printed instead.
	instructionsPaths = []string{
		"lib/shelf-self-update-instructions.toml",
		"lib/shelf/shelf-self-update-instructions.toml",
		"lib64/shelf/shelf-self-update-instructions.toml",
	}
)

// selfUpdateAvailable reports whether this install may replace itself.
func selfUpdateAvailable(prefix string) bool {
	if value, ok := os.LookupEnv("SHELF_SELF_UPDATE_AVAILABLE"); ok {
		// A malformed value falls through to the marker and instructions checks.
		if enabled, err := strconv.ParseBool(value); err == nil {
			return enabled
		}
	}
	return disableMarker(prefix) == "" && instructionsFile(prefix) == ""
}

// disableMarker returns the first marker file present under prefix, if any.
func disableMarker(prefix string) string {
	return firstExisting(prefix, disableMarkerPaths)
}

// instructionsFile returns the path packagers ship update instructions at: the
// explicit SHELF_SELF_UPDATE_INSTRUCTIONS override even when it is missing,
// otherwise the first file present under prefix.
func instructionsFile(prefix string) string {
	if path := os.Getenv("SHELF_SELF_UPDATE_INSTRUCTIONS"); path != "" {
		return path
	}
	return firstExisting(prefix, instructionsPaths)
}

// firstExisting returns the first candidate under prefix that exists.
func firstExisting(prefix string, paths []string) string {
	for _, relative := range paths {
		candidate := filepath.Join(prefix, relative)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

// installPrefix derives the prefix a package manager installed into: symlinks
// resolved, then two levels up, so /usr/bin/shelf yields /usr.
func installPrefix(target string) string {
	resolved := target
	if canonical, err := filepath.EvalSymlinks(target); err == nil {
		resolved = canonical
	}
	return filepath.Dir(filepath.Dir(resolved))
}

// selfUpdateDisabledHint is shown when self-update is disabled and the packager
// shipped no instructions; being unable to self-update is not by itself proof a
// package manager owns the install.
const selfUpdateDisabledHint = "self-update is disabled for this install, update shelf the same way you installed it"

// selfUpdateDisabledMessage explains how to update when self-update is
// unavailable: the packager's instructions when it shipped some, otherwise the
// neutral hint.
func selfUpdateDisabledMessage(prefix string) string {
	if instructions := instructionsMessage(instructionsFile(prefix)); instructions != "" {
		return instructions
	}
	return selfUpdateDisabledHint
}

// instructionsMessage reads a packager's TOML instructions: the message key, or
// the first command value when only named commands are given.
func instructionsMessage(path string) string {
	if path == "" {
		return ""
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var parsed map[string]any
	if err := toml.Unmarshal(contents, &parsed); err != nil {
		return ""
	}
	if message, ok := parsed["message"].(string); ok {
		return message
	}
	for _, key := range slices.Sorted(maps.Keys(parsed)) {
		if command, ok := parsed[key].(string); ok {
			return command
		}
	}
	return ""
}

// replaceError mirrors mise's guidance: name the stuck path, then offer sudo or
// the package manager, plus the packager's instructions when it shipped some.
func replaceError(target, directory, prefix string, sticky bool) error {
	cause := fmt.Sprintf("%s is not writable by the current user", directory)
	if sticky {
		cause = fmt.Sprintf("%s is sticky and %s belongs to another user, so only its owner can replace it", directory, target)
	}
	message := fmt.Sprintf("cannot replace %s: %s\n\nEither run `sudo shelf self-update` to update this install, or update shelf the same way you installed it.", target, cause)
	if instructions := instructionsMessage(instructionsFile(prefix)); instructions != "" {
		message += "\n\n" + instructions
	}
	return errors.New(message)
}
