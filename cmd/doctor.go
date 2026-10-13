package cmd

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/aic0d3r/ddcdeck/internal/diag"
)

// doctor prints the whole chain a first command depends on (via the daemon when it runs,
// in-process otherwise), each failure with its fix. Read-only; exits 1 when broken.
func init() {
	rootCmd.AddCommand(&cobra.Command{
		Use:   "doctor",
		Short: "Check everything ddcdeck needs, and how to fix what is missing",
		Args:  cobra.NoArgs,
		RunE:  func(*cobra.Command, []string) error { return doctor() },
	})
}

func doctor() error {
	var cs []diag.Check
	if err := call("doctor", map[string]any{}, &cs); err != nil {
		return err
	}
	if jsonFlag {
		return printJSON(cs)
	}
	failed := false
	for _, c := range cs {
		switch c.State {
		case diag.OK:
			fmt.Printf("[ ok ] %-16s %s\n", c.Name, c.Detail)
		case diag.WARN:
			fmt.Printf("[warn] %-16s %s\n", c.Name, c.Detail)
		default:
			failed = true
			fmt.Printf("[FAIL] %-16s %s\n", c.Name, c.Detail)
		}
	}
	if failed {
		return errors.New("some checks failed — follow the hints above")
	}
	fmt.Println("all good")
	return nil
}
