package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/aic0d3r/ddcdeck/internal/deck"
	"github.com/aic0d3r/ddcdeck/internal/model"
)

// modelID resolves the model an argument names, else the selected monitor's.
func modelID(a []string) (string, error) {
	if id := strings.Join(a, " "); id != "" {
		return id, nil
	}
	var mon deck.Monitor
	if err := call("monitor", deck.Params{}, &mon); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s-%s-%d", mon.Mfg, mon.Model, mon.Product), nil
}

func init() {
	m := &cobra.Command{Use: "model", Short: "Model file utilities"}
	m.AddCommand(&cobra.Command{
		Use:   "export-mccs [MFG-MODEL-PRODUCT]",
		Short: "Write the ddcutil user-defined features file for a model",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, a []string) error {
			id, err := modelID(a)
			if err != nil {
				return err
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
	m.AddCommand(&cobra.Command{
		Use:   "submit [MFG-MODEL-PRODUCT]",
		Short: "Share this monitor's model file: opens a prefilled GitHub issue",
		Long: "Opens the browser on a GitHub issue that already contains the whole model file.\n" +
			"Review it, press Submit, and a maintainer merges it into data/models/ — that is how\n" +
			"the community database grows. Nothing is uploaded until you press Submit there.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, a []string) error {
			id, err := modelID(a)
			if err != nil {
				return err
			}
			path := filepath.Join(model.UserModelsDir(), id+".toml")
			b, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("nothing discovered for %s yet — run `ddcdeck discover` first", id)
			}
			mm, err := model.LoadProfileModel(path)
			if err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
			u := mm.SubmitURL(string(b))
			fmt.Println(u) // printed first: the copy-paste path when there is no browser
			if err := exec.Command("xdg-open", u).Start(); err != nil {
				fmt.Println("Open the URL in a browser to file the issue.")
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
