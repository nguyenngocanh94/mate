package main

import (
	_ "embed"
	"os"
	"runtime"
)

//go:embed expected-skips.txt
var expectedSkipsFile string

func main() {
	os.Exit(run(os.Stdin, os.Stdout, os.Stderr, runtime.GOOS, expectedSkipsFile, os.Getenv("MATE_SKIP_REPORT")))
}
