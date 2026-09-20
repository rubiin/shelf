package lock

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

func selectFiles(directory string, patterns []string) ([]string, error) {
	if len(patterns) == 0 {
		patterns = []string{"*.sh", "*.bash", "*.zsh"}
	}
	seen := map[string]bool{}
	var files []string
	for _, pattern := range patterns {
		matches, err := doublestar.Glob(os.DirFS(directory), pattern)
		if err != nil {
			return nil, err
		}
		sort.Strings(matches)
		for _, match := range matches {
			match = filepath.Join(directory, match)
			if strings.HasSuffix(match, "/") || seen[match] {
				continue
			}
			seen[match] = true
			files = append(files, match)
		}
	}
	return files, nil
}
