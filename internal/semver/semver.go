package semver

import (
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

func Parse(version string) (*Version, error) {
	version = strings.TrimPrefix(version, "v")

	semVerRegex := regexp.MustCompile(`^(\d+)(?:\.(\d+))?(?:\.(\d+))?(-[0-9A-Za-z-.]*)?(\+[0-9A-Za-z-.]*)?$`)
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

	return strings.Compare(v1.Prerelease, v2.Prerelease)
}
