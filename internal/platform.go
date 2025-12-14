package grip

import (
	"net/url"
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

// ParseRepoPath extracts owner and repo name from various GitHub URL formats:
//   - github.com/owner/repo
//   - https://github.com/owner/repo
//   - https://github.com/owner/repo.git
func ParseRepoPath(repo string) (owner, name string, err error) {
	repo = strings.TrimSpace(repo)
	if repo == "" {
		return "", "", ErrInvalidRepo
	}

	// Handle URLs with scheme
	if strings.Contains(repo, "://") {
		u, err := url.Parse(repo)
		if err != nil || u.Host != "github.com" {
			return "", "", ErrInvalidRepo
		}
		repo = "github.com" + u.Path
	}

	// Normalize
	repo = strings.TrimSuffix(repo, ".git")
	repo = strings.TrimSuffix(repo, "/")

	parts := strings.Split(repo, "/")
	if len(parts) != 3 || parts[0] != "github.com" {
		return "", "", ErrInvalidRepo
	}

	owner, name = parts[1], parts[2]
	if owner == "" || name == "" {
		return "", "", ErrInvalidRepo
	}

	return owner, name, nil
}
