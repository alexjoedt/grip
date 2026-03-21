package grip

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

// Download downloads a file from the given URL into destDir/filename.
func Download(ctx context.Context, client *http.Client, url, destDir, filename string) error {
	if client == nil {
		client = &http.Client{}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download file: %w", err)
	}
	defer func() {
		_ = res.Body.Close()
	}()

	if res.StatusCode > 299 {
		return fmt.Errorf("download failed with status %s", res.Status)
	}

	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("create download directory: %w", err)
	}

	fullPath := filepath.Join(destDir, filename)
	f, err := os.Create(fullPath)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer func() {
		_ = f.Close()
	}()

	bar := NewProgressBar(int(res.ContentLength), "[cyan][1/3][reset] Downloading")
	if _, err = io.Copy(io.MultiWriter(f, bar), res.Body); err != nil {
		return fmt.Errorf("write file: %w", err)
	}

	fmt.Println() // new line after progress bar
	return nil
}
