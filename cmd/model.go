package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/aic0d3r/ddcdeck/internal/deck"
	"github.com/aic0d3r/ddcdeck/internal/model"
)

func init() {
	m := &cobra.Command{Use: "model", Short: "Model file utilities"}
	m.AddCommand(&cobra.Command{
		Use:   "export-mccs [MFG-MODEL-PRODUCT]",
		Short: "Write the ddcutil user-defined features file for a model",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, a []string) error {
			id := strings.Join(a, " ")
			if id == "" {
				var mon deck.Monitor
				if err := call("monitor", deck.Params{}, &mon); err != nil {
					return err
				}
				id = fmt.Sprintf("%s-%s-%d", mon.Mfg, mon.Model, mon.Product)
			}
			parts := strings.Split(id, "-")
			if len(parts) != 3 {
				return fmt.Errorf("expected MFG-MODEL-PRODUCT, got %q", id)
			}
			models, err := model.Load()
			if err != nil {
				fmt.Fprintln(os.Stderr, "ddcdeck:", err)
			}
			mm := model.Match(models, parts[0], parts[1], atoiDefault(parts[2]))
			if mm == nil {
				return &deck.Error{Code: deck.ErrUnsupported, Message: "no model file for " + id}
			}
			if err := os.MkdirAll(model.DdcutilDir(), 0o700); err != nil {
				return err
			}
			path := filepath.Join(model.DdcutilDir(), id+".mccs")
			if err := os.WriteFile(path, []byte(mm.MCCS()), 0o600); err != nil {
				return err
			}
			if !quietFlag {
				fmt.Println(path)
			}
			return nil
		},
	})
	rootCmd.AddCommand(m)
}

func atoiDefault(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
