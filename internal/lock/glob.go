package lock

import (
	"os"
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
	seen := map[string]bool{}
	var files []string
	for _, pattern := range patterns {
		rendered := strings.ReplaceAll(pattern, "{{ name }}", name)
		matches, err := doublestar.Glob(os.DirFS(directory), rendered)
		if err != nil {
			return nil, err
		}
		if len(matches) == 0 {
			continue
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
		if firstMatch {
			break
		}
	}
	return files, nil
}
