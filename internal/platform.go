package grip

import (
	"strings"
)

// MatchesPlatform checks if filename contains given OS and Arch (or aliases)
func MatchesPlatform(filename, targetOS, targetArch string, osAliases, archAliases map[string][]string) bool {
	name := strings.ToLower(filename)

	// Check OS
	osMatches := strings.Contains(name, targetOS)
	if !osMatches {
		if aliases, ok := osAliases[targetOS]; ok {
			for _, alias := range aliases {
				if strings.Contains(name, alias) {
					osMatches = true
					break
				}
			}
		}
	}

	// Check Arch
	archMatches := strings.Contains(name, targetArch)
	if !archMatches {
		if aliases, ok := archAliases[targetArch]; ok {
			for _, alias := range aliases {
				if strings.Contains(name, alias) {
					archMatches = true
					break
				}
			}
		}
	}

	return osMatches && archMatches
}

// ParseRepoPath extracts owner and repo name from github.com/owner/repo format
func ParseRepoPath(repo string) (owner, name string, err error) {
	if !strings.HasPrefix(repo, "github.com") {
		return "", "", ErrInvalidRepo
	}

	parts := strings.Split(repo, "/")
	if len(parts) != 3 {
		return "", "", ErrInvalidRepo
	}

	return parts[1], parts[2], nil
}
