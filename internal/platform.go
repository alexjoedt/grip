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

// Repo is a normalized package identity.
type Repo struct {
	Host  string
	Owner string
	Name  string
}

// String returns the canonical form host/owner/repo.
func (r Repo) String() string {
	return r.Host + "/" + r.Owner + "/" + r.Name
}

// ParseRepo parses a package reference into a Repo. Accepted forms:
//   - owner/repo
//   - github.com/owner/repo
//   - https://github.com/owner/repo(.git)(/)
func ParseRepo(ref string) (Repo, error) {
	ref = strings.TrimSpace(ref)
	if strings.Contains(ref, "://") {
		u, err := url.Parse(ref)
		if err != nil {
			return Repo{}, ErrInvalidRepo
		}
		ref = u.Host + u.Path
	}
	ref = strings.TrimSuffix(ref, "/")
	ref = strings.TrimSuffix(ref, ".git")

	parts := strings.Split(ref, "/")
	// GitHub owners cannot contain dots, so a dotted first segment is a host.
	if len(parts) == 2 && !strings.Contains(parts[0], ".") {
		parts = append([]string{"github.com"}, parts...)
	}
	if len(parts) != 3 || parts[0] != "github.com" || parts[1] == "" || parts[2] == "" {
		return Repo{}, ErrInvalidRepo
	}

	return Repo{Host: parts[0], Owner: parts[1], Name: parts[2]}, nil
}
