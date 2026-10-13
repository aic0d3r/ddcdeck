// ddcdeck — control monitor features over DDC/CI. Invoked as ddcdeckd it runs the daemon.
package main

import (
	"os"
	"path/filepath"

	"github.com/aic0d3r/ddcdeck/cmd"
)

func main() {
	args := os.Args[1:]
	if filepath.Base(os.Args[0]) == "ddcdeckd" {
		args = append([]string{"daemon", "run"}, args...)
	}
	os.Exit(cmd.Execute(args))
}
