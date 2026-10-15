package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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
	report := &cobra.Command{
		Use:   "report works|partial|broken [MFG-MODEL-PRODUCT]",
		Short: "Tell the community whether this monitor's model file worked",
		Long: "Opens a prefilled GitHub issue with your verdict. Reports are how unverified model\n" +
			"files become verified. Nothing is sent until you press Submit in the browser.",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(_ *cobra.Command, a []string) error {
			if !slices.Contains(model.Verdicts, a[0]) {
				return fmt.Errorf("verdict must be one of %s", strings.Join(model.Verdicts, ", "))
			}
			id, err := modelID(a[1:])
			if err != nil {
				return err
			}
			u := model.ReportURL(id, a[0], "ddcdeck "+Version)
			fmt.Println(u)
			if err := exec.Command("xdg-open", u).Start(); err != nil {
				fmt.Println("Open the URL in a browser to file the report.")
			}
			return nil
		},
	}
	m.AddCommand(report)
	m.AddCommand(&cobra.Command{
		Use:   "fetch [MFG-MODEL-PRODUCT]",
		Short: "Download a community model file for this monitor (opt-in lookup)",
		Long: "Pulls one model file from the repo's data/models/ into ~/.local/share/ddcdeck/models/,\n" +
			"validates it, and rescans. Nothing is looked up unless you ask: ddcdeck is offline-first.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, a []string) error {
			id, err := modelID(a)
			if err != nil {
				return err
			}
			var res struct {
				Path string `json:"path"`
			}
			if err := call("model.fetch", map[string]any{"id": id}, &res); err != nil {
				return err
			}
			if !quietFlag {
				fmt.Println(res.Path)
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
