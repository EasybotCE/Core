package main

import (
	"os"

	"easybot/internal/bootstrap"
)

func main() {
	if code := bootstrap.Run(); code != 0 {
		bootstrap.WaitAfterFailure()
		os.Exit(code)
	}
}
