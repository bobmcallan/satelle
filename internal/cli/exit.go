package cli

import (
	"errors"
	"fmt"
)

// ExitError is a command failure that carries its own process exit code. Every
// other error a command returns exits 1; a command whose outcome is not simply
// pass or fail (a drive that parked on a question) returns one of these so a
// script can tell the outcomes apart. cmd/satelle reads the code through
// ExitCodeOf.
type ExitError struct {
	Code int
	Err  error
	// Quiet says the command already reported everything on its own output, so
	// Execute does not print the error again.
	Quiet bool
}

func (e *ExitError) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("exit status %d", e.Code)
	}
	return e.Err.Error()
}

func (e *ExitError) Unwrap() error { return e.Err }

// ExitCode is the process exit code this error asks for.
func (e *ExitError) ExitCode() int { return e.Code }

// ExitCodeOf maps a command error to the process exit code: the code an
// ExitError in its chain carries, else 1 for any other error.
func ExitCodeOf(err error) int {
	var ec interface{ ExitCode() int }
	if errors.As(err, &ec) {
		return ec.ExitCode()
	}
	return 1
}
