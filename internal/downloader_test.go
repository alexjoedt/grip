package grip

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
			err := Download(ctx, httpClient, tc.downloadURL, destDir, tc.filename)

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
			}

			mockTransport.AssertExpectations(t)
		})
	}
}
