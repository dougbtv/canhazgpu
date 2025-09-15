package main

import (
	"fmt"
	"os"

	"github.com/russellb/canhazgpu/internal/k8scli"
)

func main() {
	if err := k8scli.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}