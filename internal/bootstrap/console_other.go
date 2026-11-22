//go:build !windows

package bootstrap

import "os"

func enableColors() bool {
	info, err := os.Stdout.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0 && os.Getenv("TERM") != "dumb"
}

func WaitAfterFailure() {}
