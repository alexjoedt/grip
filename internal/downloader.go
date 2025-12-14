package grip

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

// Downloader handles downloading files from URLs
type Downloader struct {
	client *http.Client
}

// NewDownloader creates a new Downloader with the given HTTP client
func NewDownloader(client *http.Client) *Downloader {
	if client == nil {
		client = &http.Client{}
	}
	return &Downloader{
		client: client,
	}
}

// Download downloads a file from the given URL to the destination path
// Returns the full path to the downloaded file
func (d *Downloader) Download(ctx context.Context, url, destPath, filename string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	res, err := d.client.Do(req)
	if err != nil {
		return fmt.Errorf("download file: %w", err)
	}
	defer func() {
		if closeErr := res.Body.Close(); closeErr != nil {
			// TODO: use logger when available
			_ = closeErr
		}
	}()

	if res.StatusCode > 299 {
		return fmt.Errorf("download failed with status %s", res.Status)
	}

	if err := os.MkdirAll(destPath, 0755); err != nil {
		return fmt.Errorf("create download directory: %w", err)
	}

	fullPath := filepath.Join(destPath, filename)
	f, err := os.Create(fullPath)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer func() {
		if closeErr := f.Close(); closeErr != nil {
			_ = closeErr
		}
	}()

	bar := NewProgressBar(int(res.ContentLength), "[cyan][1/3][reset] Downloading")
	_, err = io.Copy(io.MultiWriter(f, bar), res.Body)
	if err != nil {
		return fmt.Errorf("write file: %w", err)
	}

	fmt.Println() // new line after progress bar
	return nil
}
