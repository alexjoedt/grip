package grip

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockRoundTripper is a mock implementation of http.RoundTripper for testing HTTP clients
type MockRoundTripper struct {
	mock.Mock
}

func (m *MockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	args := m.Called(req)
	if resp := args.Get(0); resp != nil {
		return resp.(*http.Response), args.Error(1)
	}
	return nil, args.Error(1)
}

// Helper to create a mock HTTP client
func newMockHTTPClient(transport http.RoundTripper) *http.Client {
	return &http.Client{
		Transport: transport,
	}
}

// Test helpers and fixtures

// createMockResponse creates a mock HTTP response
func createMockResponse(statusCode int, body string, contentLength int64) *http.Response {
	return &http.Response{
		StatusCode:    statusCode,
		Status:        http.StatusText(statusCode),
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: contentLength,
	}
}

// Test Downloader service
func TestDownloader(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name           string
		filename       string
		downloadURL    string
		mockSetup      func(*MockRoundTripper)
		expectError    bool
		expectedErrMsg string
	}{
		{
			name:        "successful download",
			filename:    "test.tar.gz",
			downloadURL: "https://example.com/test.tar.gz",
			mockSetup: func(m *MockRoundTripper) {
				resp := createMockResponse(200, "test file content", 17)
				m.On("RoundTrip", mock.Anything).Return(resp, nil)
			},
			expectError: false,
		},
		{
			name:        "HTTP 404 error",
			filename:    "missing.tar.gz",
			downloadURL: "https://example.com/missing.tar.gz",
			mockSetup: func(m *MockRoundTripper) {
				resp := createMockResponse(404, "Not Found", 9)
				m.On("RoundTrip", mock.Anything).Return(resp, nil)
			},
			expectError:    true,
			expectedErrMsg: "download failed with status",
		},
		{
			name:        "network error",
			filename:    "network.tar.gz",
			downloadURL: "https://example.com/network.tar.gz",
			mockSetup: func(m *MockRoundTripper) {
				m.On("RoundTrip", mock.Anything).Return(nil, fmt.Errorf("connection refused"))
			},
			expectError:    true,
			expectedErrMsg: "connection refused",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockTransport := new(MockRoundTripper)
			tc.mockSetup(mockTransport)

			httpClient := newMockHTTPClient(mockTransport)
			destDir := filepath.Join(os.TempDir(), "test-download-"+tc.filename)
			defer os.RemoveAll(destDir)

			ctx := context.Background()
			sum, err := Download(ctx, httpClient, tc.downloadURL, destDir, tc.filename, 0)

			if tc.expectError {
				assert.Error(t, err)
				if tc.expectedErrMsg != "" {
					assert.Contains(t, err.Error(), tc.expectedErrMsg)
				}
			} else {
				assert.NoError(t, err)
				downloadPath := filepath.Join(destDir, tc.filename)
				assert.FileExists(t, downloadPath)

				content, err := os.ReadFile(downloadPath)
				assert.NoError(t, err)
				assert.Equal(t, "test file content", string(content))
				want := sha256.Sum256(content)
				assert.Equal(t, hex.EncodeToString(want[:]), sum)
			}

			mockTransport.AssertExpectations(t)
		})
	}
}

func TestDownloadBounds(t *testing.T) {
	defer func(d time.Duration) { stallTimeout = d }(stallTimeout)
	stallTimeout = 200 * time.Millisecond

	chunk := []byte("0123456789")
	tests := map[string]struct {
		size    int64
		handler http.HandlerFunc
		wantErr string
	}{
		"slow but steady": {size: 100, handler: func(w http.ResponseWriter, _ *http.Request) {
			for range 10 {
				_, _ = w.Write(chunk)
				w.(http.Flusher).Flush()
				time.Sleep(50 * time.Millisecond)
			}
		}},
		"stalled": {handler: func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write(chunk)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		}, wantErr: "stalled"},
		"stalled before headers": {handler: func(_ http.ResponseWriter, r *http.Request) {
			<-r.Context().Done()
		}, wantErr: "stalled"},
		"exceeds declared size": {size: 5, handler: func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write(bytes.Repeat(chunk, 10))
		}, wantErr: "exceeds declared size"},
		"truncated": {size: 100, handler: func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write(chunk)
		}, wantErr: "truncated"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(tt.handler)
			defer srv.Close()
			dir := t.TempDir()

			_, err := Download(context.Background(), srv.Client(), srv.URL, dir, "asset", tt.size)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want %q", err, tt.wantErr)
			}
			if _, err := os.Stat(filepath.Join(dir, "asset")); !os.IsNotExist(err) {
				t.Errorf("partial file left behind: %v", err)
			}
		})
	}
}

func TestDownloadCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("partial"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer srv.Close()
	dir := t.TempDir()

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	start := time.Now()
	_, err := Download(ctx, srv.Client(), srv.URL, dir, "asset", 0)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("cancel took %s", d)
	}
	if _, err := os.Stat(filepath.Join(dir, "asset")); !os.IsNotExist(err) {
		t.Errorf("partial file left behind: %v", err)
	}
}
