package terminal

import (
	"fmt"
	"os"
)

// Success prints a success message in green.
func Success(msg string) {
	fmt.Printf("%s%s%s\n", ColorGreen, msg, ColorReset)
}

// Successf prints a formatted success message in green.
func Successf(format string, a ...any) {
	fmt.Printf("%s%s%s\n", ColorGreen, fmt.Sprintf(format, a...), ColorReset)
}

// Info prints an informational message in blue.
func Info(msg string) {
	fmt.Printf("%s%s%s\n", ColorBlue, msg, ColorReset)
}

// Infof prints a formatted informational message in blue.
func Infof(format string, a ...any) {
	fmt.Printf("%s%s%s\n", ColorBlue, fmt.Sprintf(format, a...), ColorReset)
}

// Warning prints a warning message in yellow.
func Warning(msg string) {
	fmt.Printf("%s%s%s\n", ColorYellow, msg, ColorReset)
}

// Warningf prints a formatted warning message in yellow.
func Warningf(format string, a ...any) {
	fmt.Printf("%s%s%s\n", ColorYellow, fmt.Sprintf(format, a...), ColorReset)
}

// Error prints an error message in red to stderr.
func Error(msg string) {
	fmt.Fprintf(os.Stderr, "%s%s%s\n", ColorRed, msg, ColorReset)
}

// Errorf prints a formatted error message in red to stderr.
func Errorf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "%s%s%s\n", ColorRed, fmt.Sprintf(format, a...), ColorReset)
}

// Fatal prints an error message in red and exits.
func Fatal(msg string) {
	Error(msg)
	os.Exit(1)
}

// Fatalf prints a formatted error message in red and exits.
func Fatalf(format string, a ...any) {
	Errorf(format, a...)
	os.Exit(1)
}
