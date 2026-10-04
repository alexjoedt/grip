package logger

import (
	"fmt"
	"os"
)

var (
	// verboseMode controls whether Info messages are displayed
	verboseMode = false
	// quietMode suppresses Success and Println status lines
	quietMode = false
)

// SetVerbose enables or disables verbose logging
func SetVerbose(verbose bool) {
	verboseMode = verbose
}

// IsVerbose returns the current verbose mode status
func IsVerbose() bool {
	return verboseMode
}

// SetQuiet enables or disables quiet mode
func SetQuiet(quiet bool) {
	quietMode = quiet
}

// IsQuiet returns the current quiet mode status
func IsQuiet() bool {
	return quietMode
}

// Info prints informational messages only when verbose mode is enabled
func Info(format string, args ...interface{}) {
	if verboseMode {
		fmt.Fprintf(os.Stderr, "[INFO] "+format+"\n", args...)
	}
}

// Success prints success messages to stderr unless quiet
func Success(format string, args ...interface{}) {
	if !quietMode {
		fmt.Fprintf(os.Stderr, "[SUCCESS] "+format+"\n", args...)
	}
}

// Warn prints warning messages to stderr - always shown
func Warn(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "[WARN] "+format+"\n", args...)
}

// Error prints error messages to stderr without exiting
func Error(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "[ERROR] "+format+"\n", args...)
}

// Print prints a prompt to stderr without any prefix, also when quiet
func Print(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format, args...)
}

// Println prints a status line to stderr unless quiet
func Println(format string, args ...interface{}) {
	if !quietMode {
		fmt.Fprintf(os.Stderr, format+"\n", args...)
	}
}
