package logger

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestInfo(t *testing.T) {
	// Test that Info shows output when verbose is true
	SetVerbose(true)
	defer SetVerbose(false)
	
	// Capture stderr
	origStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w
	defer func() {
		os.Stderr = origStderr
	}()

	Info("test message")
	
	w.Close()
	buf := make([]byte, 1024)
	n, _ := r.Read(buf)
	output := string(buf[:n])
	
	assert.Contains(t, output, "test message")
	assert.Contains(t, output, "[INFO]")
}

func TestInfoVerboseOff(t *testing.T) {
	// Test that Info shows no output when verbose is false
	SetVerbose(false)
	
	// Capture stderr
	origStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w
	defer func() {
		os.Stderr = origStderr
	}()

	Info("test message")
	
	w.Close()
	buf := make([]byte, 1024)
	n, _ := r.Read(buf)
	output := string(buf[:n])
	
	assert.Empty(t, output)
}

func TestWarn(t *testing.T) {
	// Capture stderr
	origStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w
	defer func() {
		os.Stderr = origStderr
	}()

	Warn("test warning")
	
	w.Close()
	buf := make([]byte, 1024)
	n, _ := r.Read(buf)
	output := string(buf[:n])
	
	assert.Contains(t, output, "[WARN]")
	assert.Contains(t, output, "test warning")
}

func TestSuccess(t *testing.T) {
	// Capture stdout
	origStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() {
		os.Stdout = origStdout
	}()

	Success("operation completed")
	
	w.Close()
	buf := make([]byte, 1024)
	n, _ := r.Read(buf)
	output := string(buf[:n])
	
	assert.Contains(t, output, "[SUCCESS]")
	assert.Contains(t, output, "operation completed")
}

func TestError(t *testing.T) {
	// Capture stderr
	origStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w
	defer func() {
		os.Stderr = origStderr
	}()

	Error("test error")
	
	w.Close()
	buf := make([]byte, 1024)
	n, _ := r.Read(buf)
	output := string(buf[:n])
	
	assert.Contains(t, output, "[ERROR]")
	assert.Contains(t, output, "test error")
}
