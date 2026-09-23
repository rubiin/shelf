package lock

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

func defaultMatches(shell string) []string {
	if shell == "zsh" {
		return []string{
			"{{ name }}.plugin.zsh",
			"{{ name }}.zsh",
			"{{ name }}.sh",
			"{{ name }}.zsh-theme",
			"*.plugin.zsh",
			"*.zsh",
			"*.sh",
			"*.zsh-theme",
		}
	}
	return []string{
		"{{ name }}.plugin.bash",
		"{{ name }}.plugin.sh",
		"{{ name }}.bash",
		"{{ name }}.sh",
		"*.plugin.bash",
		"*.plugin.sh",
		"*.bash",
		"*.sh",
	}
}

func selectFiles(directory, name, shell string, patterns []string, firstMatch bool, ignored []string) ([]string, error) {
	if len(patterns) == 0 {
		patterns = defaultMatches(shell)
		firstMatch = true
	}
	candidates, err := collectFiles(directory)
	if err != nil {
		return nil, err
	}
	// A name is text, not glob syntax: escape its metacharacters so a quoted
	// plugin name can't widen the selection it matches.
	escapedName := escapeGlob(name)
	rendered := make([]string, len(patterns))
	for index, pattern := range patterns {
		rendered[index] = strings.ReplaceAll(pattern, "{{ name }}", escapedName)
		// Clean like the walk output so `./*.zsh` still matches relative paths.
		rendered[index] = path.Clean(rendered[index])
		// Validate every pattern before selection so a typo fails even when an
		// earlier first-match pattern already selects files.
		if !doublestar.ValidatePattern(rendered[index]) {
			return nil, fmt.Errorf("invalid pattern: %s", pattern)
		}
	}
	var matched []string
	for _, pattern := range rendered {
		selected := 0
		for _, candidate := range candidates {
			ok, err := doublestar.Match(pattern, candidate)
			if err != nil {
				return nil, err
			}
			if ok {
				matched = append(matched, candidate)
				selected++
			}
		}
		if firstMatch && selected > 0 {
			break
		}
	}
	// Drop ignored matches so test trees never load.
	matched, err = dropIgnored(matched, name, ignored)
	if err != nil {
		return nil, err
	}
	// Patterns match together, so results are ordered by file name.
	sort.SliceStable(matched, func(left, right int) bool {
		return filepath.Base(matched[left]) < filepath.Base(matched[right])
	})
	seen := map[string]bool{}
	var files []string
	for _, match := range matched {
		match = filepath.Join(directory, match)
		if seen[match] {
			continue
		}
		seen[match] = true
		files = append(files, match)
	}
	return files, nil
}

// dropIgnored applies the same `{{ name }}` substitution and up-front validation as use patterns.
func dropIgnored(matched []string, name string, ignored []string) ([]string, error) {
	if len(ignored) == 0 {
		return matched, nil
	}
	patterns := make([]string, len(ignored))
	escapedName := escapeGlob(name)
	for index, pattern := range ignored {
		rendered := strings.ReplaceAll(pattern, "{{ name }}", escapedName)
		rendered = path.Clean(rendered)
		if !doublestar.ValidatePattern(rendered) {
			return nil, fmt.Errorf("invalid ignore pattern: %s", pattern)
		}
		patterns[index] = rendered
	}
	var kept []string
	for _, candidate := range matched {
		drop := false
		for _, pattern := range patterns {
			ok, err := doublestar.Match(pattern, candidate)
			if err != nil {
				return nil, err
			}
			if ok {
				drop = true
				break
			}
		}
		if !drop {
			kept = append(kept, candidate)
		}
	}
	return kept, nil
}

// collectFiles walks once, returning slash-separated relative paths.
func collectFiles(directory string) ([]string, error) {
	// A missing directory yields an empty selection; other failures are reported.
	if _, err := os.Stat(directory); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	// Resolve the root so a symlinked plugin directory (stow-style local sources)
	// is walked as the directory it points to, not as a single "file".
	resolved, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return nil, err
	}
	var files []string
	err = filepath.WalkDir(resolved, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			// .git can't match a source pattern; skip it entirely.
			if entry.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		relative, err := filepath.Rel(resolved, path)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(relative))
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

// escapeGlob backslash-escapes the characters doublestar treats as metacharacters,
// so a plugin name in a pattern is matched literally.
func escapeGlob(value string) string {
	var builder strings.Builder
	builder.Grow(len(value))
	for _, character := range value {
		switch character {
		case '\\', '*', '?', '[', ']', '{', '}':
			builder.WriteByte('\\')
		}
		builder.WriteRune(character)
	}
	return builder.String()
}
