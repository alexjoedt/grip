package logger

import (
	"fmt"
	"os"
)

var (
	// verboseMode controls whether Info messages are displayed
	verboseMode = false
)

// SetVerbose enables or disables verbose logging
func SetVerbose(verbose bool) {
	verboseMode = verbose
}

// IsVerbose returns the current verbose mode status
func IsVerbose() bool {
	return verboseMode
}

// Info prints informational messages only when verbose mode is enabled
func Info(format string, args ...interface{}) {
	if verboseMode {
		fmt.Fprintf(os.Stderr, "[INFO] "+format+"\n", args...)
	}
}

// Success prints success messages to stdout
func Success(format string, args ...interface{}) {
	fmt.Fprintf(os.Stdout, "[SUCCESS] "+format+"\n", args...)
}

// Warn prints warning messages to stderr - always shown
func Warn(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "[WARN] "+format+"\n", args...)
}

// Error prints error messages to stderr without exiting
func Error(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "[ERROR] "+format+"\n", args...)
}

// Fatal prints error message to stderr and exits with code 1
func Fatal(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "[FATAL] "+format+"\n", args...)
	os.Exit(1)
}

// Print prints messages to stdout without any prefix (for formatted output like tables)
func Print(format string, args ...interface{}) {
	fmt.Fprintf(os.Stdout, format, args...)
}

// Println prints messages to stdout with a newline
func Println(format string, args ...interface{}) {
	fmt.Fprintf(os.Stdout, format+"\n", args...)
}
