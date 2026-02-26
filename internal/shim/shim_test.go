package shim

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsTTY(t *testing.T) {
	// go test may run with stdin as a TTY (interactive) or pipe (CI).
	// We can only reliably test that the function doesn't panic.
	fi, err := os.Stdin.Stat()
	if err != nil {
		t.Skip("cannot stat stdin")
	}
	isCharDevice := fi.Mode()&os.ModeCharDevice != 0
	assert.Equal(t, isCharDevice, isTTY())
}
