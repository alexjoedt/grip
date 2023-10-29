package grip

import (
	"net/url"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetLatest(t *testing.T) {
	var (
		owner string = "alexjoedt"
		repo  string = "grip"
	)

	asset, err := GetLatest(owner, repo)

	assert.Nil(t, err)
	assert.Equal(t, repo, asset.repoName)
	assert.Equal(t, owner, asset.repoOwner)
	assert.Equal(t, runtime.GOOS, asset.OS)
	assert.Equal(t, runtime.GOARCH, asset.Arch)
	assert.NotEmpty(t, asset.Tag)

	_, err = url.ParseRequestURI(asset.DownloadURL)
	if err != nil {
		t.Errorf("DownloadURL is not valid: %s: %v\n", asset.DownloadURL, err)
	}
}

func TestGetByTag(t *testing.T) {
	var (
		owner string = "alexjoedt"
		repo  string = "grip"
		tag   string = "v0.1.0-alpha.1"
	)
	asset, err := GetByTag(owner, repo, tag)

	assert.Nil(t, err)
	assert.Equal(t, repo, asset.repoName)
	assert.Equal(t, owner, asset.repoOwner)
	assert.Equal(t, tag, asset.Tag)
	assert.Equal(t, runtime.GOOS, asset.OS)
	assert.Equal(t, runtime.GOARCH, asset.Arch)
	assert.NotEmpty(t, asset.Tag)

	_, err = url.ParseRequestURI(asset.DownloadURL)
	if err != nil {
		t.Errorf("DownloadURL is not valid: %s: %v\n", asset.DownloadURL, err)
	}
}
