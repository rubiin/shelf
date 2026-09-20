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

func selectFiles(directory, name, shell string, patterns []string, firstMatch bool) ([]string, error) {
	if len(patterns) == 0 {
		patterns = defaultMatches(shell)
		firstMatch = true
	}
	candidates, err := collectFiles(directory)
	if err != nil {
		return nil, err
	}
	var matched []string
	for _, pattern := range patterns {
		rendered := strings.ReplaceAll(pattern, "{{ name }}", name)
		// Clean like the glob walk did, so patterns such as `./*.zsh` still match relative paths.
		rendered = path.Clean(rendered)
		// Validate up front so a typo fails even when the tree holds no candidates.
		if !doublestar.ValidatePattern(rendered) {
			return nil, fmt.Errorf("invalid pattern: %s", pattern)
		}
		selected := 0
		for _, candidate := range candidates {
			ok, err := doublestar.Match(rendered, candidate)
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
	// Every pattern is walked together, so the selection is ordered by file name.
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

// collectFiles walks directory once and returns its non-directory paths, slash-separated.
func collectFiles(directory string) ([]string, error) {
	// Only a missing plugin directory is empty; every other walk failure is reported.
	if _, err := os.Stat(directory); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var files []string
	err := filepath.WalkDir(directory, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(directory, path)
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
