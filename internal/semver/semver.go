package semver

import (
	"cmp"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type Version struct {
	Major      int
	Minor      int
	Patch      int
	Prerelease string
	Metadata   string
}

var semVerRegex = regexp.MustCompile(`^(\d+)(?:\.(\d+))?(?:\.(\d+))?(-[0-9A-Za-z-.]*)?(\+[0-9A-Za-z-.]*)?$`)

// Parse parses a version; a leading v is ignored.
func Parse(version string) (*Version, error) {
	version = strings.TrimPrefix(version, "v")

	matches := semVerRegex.FindStringSubmatch(version)

	if matches == nil {
		return nil, fmt.Errorf("invalid semver: %s", version)
	}

	major, _ := strconv.Atoi(matches[1])
	minor := 0
	patch := 0

	if matches[2] != "" {
		minor, _ = strconv.Atoi(matches[2])
	}
	if matches[3] != "" {
		patch, _ = strconv.Atoi(matches[3])
	}

	return &Version{
		Major:      major,
		Minor:      minor,
		Patch:      patch,
		Prerelease: strings.TrimPrefix(matches[4], "-"),
		Metadata:   strings.TrimPrefix(matches[5], "+"),
	}, nil
}

// Compare compares two version
// comp := compareSemVer(v1, v2)
//
//	if comp < 0 {
//		fmt.Println("v1 < v2")
//	} else if comp > 0 {
//		fmt.Println("v1 > v2")
//	} else {
//		fmt.Println("v1 == v2")
//	}
func Compare(v1, v2 *Version) int {

	if v1.Major != v2.Major {
		return v1.Major - v2.Major
	}
	if v1.Minor != v2.Minor {
		return v1.Minor - v2.Minor
	}
	if v1.Patch != v2.Patch {
		return v1.Patch - v2.Patch
	}

	if v1.Prerelease == "" && v2.Prerelease != "" {
		return 1
	}
	if v1.Prerelease != "" && v2.Prerelease == "" {
		return -1
	}

	return comparePrerelease(v1.Prerelease, v2.Prerelease)
}

// comparePrerelease orders dot-separated identifiers per semver 2.0.0:
// numeric ones numerically and below alphanumeric ones, and a shorter list
// below a longer one with the same prefix.
func comparePrerelease(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		an, aErr := strconv.ParseUint(as[i], 10, 64)
		bn, bErr := strconv.ParseUint(bs[i], 10, 64)
		var c int
		switch {
		case aErr == nil && bErr == nil:
			c = cmp.Compare(an, bn)
		case aErr == nil:
			c = -1
		case bErr == nil:
			c = 1
		default:
			c = strings.Compare(as[i], bs[i])
		}
		if c != 0 {
			return c
		}
	}
	return cmp.Compare(len(as), len(bs))
}
