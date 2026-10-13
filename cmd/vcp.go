package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/aic0d3r/ddcdeck/internal/ddc"
	"github.com/aic0d3r/ddcdeck/internal/deck"
)

func init() {
	vcp := &cobra.Command{Use: "vcp", Short: "Raw VCP access (escape hatch)"}
	vcp.AddCommand(&cobra.Command{
		Use: "get <code>", Short: "Read a VCP code (hex), bypassing the cache", Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, a []string) error {
			var v ddc.Value
			if err := call("vcp.get", deck.Params{Code: a[0]}, &v); err != nil {
				return err
			}
			if jsonFlag {
				return printJSON(v)
			}
			fmt.Printf("%d (0x%02x) max %d\n", v.Cur, v.Cur, v.Max)
			return nil
		},
	})
	vcp.AddCommand(&cobra.Command{
		Use: "set <code> <value>", Short: "Write a VCP code (hex code, decimal or 0x value)", Args: cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, a []string) error {
			raw, _ := json.Marshal(a[1])
			return call("vcp.set", deck.Params{Code: a[0], Value: raw}, nil)
		},
	})
	rootCmd.AddCommand(vcp)
}
