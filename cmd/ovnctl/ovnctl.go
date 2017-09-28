package main

import (
	"os"
	"runtime"

	"github.com/mozhuli/ovn-stackube/cmd/ovnctl/app"
)

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())
	if err := app.Run(); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}
