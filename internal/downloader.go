package grip

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// stallTimeout aborts a download that receives no data for this long.
var stallTimeout = 30 * time.Second

var errStalled = errors.New("download stalled")

// Download downloads a file from the given URL into destDir/filename and
// returns the hex SHA-256 of its bytes. A size above zero is the declared
// size; a body of any other length fails. Nothing is left behind on failure.
func Download(ctx context.Context, client *http.Client, url, destDir, filename string, size int64) (sum string, err error) {
	if client == nil {
		client = &http.Client{}
	}

	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	stalled := fmt.Errorf("%w: no data for %s", errStalled, stallTimeout)
	timer := time.AfterFunc(stallTimeout, func() { cancel(stalled) })
	defer timer.Stop()
	defer func() {
		if err != nil && context.Cause(ctx) == stalled {
			err = stalled
		}
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}

	res, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("download file: %w", err)
	}
	defer func() {
		_ = res.Body.Close()
	}()

	if res.StatusCode > 299 {
		return "", fmt.Errorf("download failed with status %s", res.Status)
	}

	if err := os.MkdirAll(destDir, 0755); err != nil {
		return "", fmt.Errorf("create download directory: %w", err)
	}

	fullPath := filepath.Join(destDir, filename)
	f, err := os.Create(fullPath)
	if err != nil {
		return "", fmt.Errorf("create file: %w", err)
	}
	defer func() {
		_ = f.Close()
		if err != nil {
			_ = os.Remove(fullPath)
		}
	}()

	var body io.Reader = stallReader{res.Body, timer}
	if size > 0 {
		body = io.LimitReader(body, size+1)
	}
	h := sha256.New()
	bar := NewProgressBar(int(res.ContentLength), "[cyan][1/3][reset] Downloading")
	n, err := io.Copy(io.MultiWriter(f, h, bar), body)
	if err != nil {
		return "", fmt.Errorf("write file: %w", err)
	}
	if size > 0 && n > size {
		return "", fmt.Errorf("%s exceeds declared size of %d bytes", filename, size)
	}
	if size > 0 && n < size {
		return "", fmt.Errorf("%s truncated: got %d of %d bytes", filename, n, size)
	}

	endProgressBar()
	return hex.EncodeToString(h.Sum(nil)), nil
}

// stallReader restarts the stall timer on every read.
type stallReader struct {
	r     io.Reader
	timer *time.Timer
}

func (s stallReader) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	s.timer.Reset(stallTimeout)
	return n, err
}
