//go:build !darwin && !linux

package resources

import (
	"context"
	"fmt"
	"runtime"
)

// Feat targets macOS and Linux (docs/08-v0-scope.md), and the daemon's process
// ownership is a Unix facility besides (ADR-027). This file exists so that the
// package still builds elsewhere and says what it cannot do, rather than failing
// to compile with an error about a missing function.

func readLoad(_ context.Context, _ Runner) ([3]float64, error) {
	return [3]float64{}, fmt.Errorf("Feat does not read machine load on %s", runtime.GOOS)
}

func readMemory(_ context.Context, _ Runner) (total, available uint64, err error) {
	return 0, 0, fmt.Errorf("Feat does not read machine memory on %s", runtime.GOOS)
}

func diskUsage(_ string) (total, available uint64, err error) {
	return 0, 0, fmt.Errorf("Feat does not read disk availability on %s", runtime.GOOS)
}
