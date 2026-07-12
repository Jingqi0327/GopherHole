package terminal

import (
	"fmt"
	"os"
)

// Success prints a success message in green.
func Success(msg string) {
	fmt.Printf("%s%s%s\n", ColorGreen, msg, ColorReset)
}

// Info prints an informational message in blue.
func Info(msg string) {
	fmt.Printf("%s%s%s\n", ColorBlue, msg, ColorReset)
}

// Warning prints a warning message in yellow.
func Warning(msg string) {
	fmt.Printf("%s%s%s\n", ColorYellow, msg, ColorReset)
}

// Error prints an error message in red to stderr.
func Error(msg string) {
	fmt.Fprintf(os.Stderr, "%s%s%s\n", ColorRed, msg, ColorReset)
}
