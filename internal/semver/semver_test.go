package semver

import (
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		input  string
		output *Version
		err    bool
	}{
		{"v1.0.0-alpha.1", &Version{1, 0, 0, "alpha.1", ""}, false},
		{"1.0.0-beta.1", &Version{1, 0, 0, "beta.1", ""}, false},
		{"1.4.2", &Version{1, 4, 2, "", ""}, false},
		{"v1.0", &Version{1, 0, 0, "", ""}, false},
		{"invalid", nil, true},
	}

	for _, test := range tests {
		result, err := Parse(test.input)
		if (err != nil) != test.err {
			t.Errorf("expected error %v, got %v for input %s", test.err, err, test.input)
			continue
		}
		if err == nil && !compareStructs(result, test.output) {
			t.Errorf("for input %s, expected %v, got %v", test.input, test.output, result)
		}
	}
}

func compareStructs(v1, v2 *Version) bool {
	return v1.Major == v2.Major &&
		v1.Minor == v2.Minor &&
		v1.Patch == v2.Patch &&
		v1.Prerelease == v2.Prerelease &&
		v1.Metadata == v2.Metadata
}

func TestCompare(t *testing.T) {
	tests := []struct {
		v1, v2 *Version
		result int
	}{
		{&Version{1, 0, 0, "alpha.1", ""}, &Version{1, 0, 0, "beta.1", ""}, -1},
		{&Version{1, 0, 0, "", ""}, &Version{1, 0, 0, "beta.1", ""}, 1},
		{&Version{1, 4, 2, "", ""}, &Version{1, 4, 2, "", ""}, 0},
	}

	for _, test := range tests {
		result := Compare(test.v1, test.v2)
		if result != test.result {
			t.Errorf("for %v and %v, expected %d, got %d", test.v1, test.v2, test.result, result)
		}
	}
}
