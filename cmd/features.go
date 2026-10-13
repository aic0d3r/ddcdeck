package cmd

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/aic0d3r/ddcdeck/internal/deck"
)

// feature commands: name → kind. Names are looked up in the monitor's resolved features.
var featureCmds = []struct {
	name, kind, short string
	onOff             bool
}{
	{"brightness", "range", "Brightness (0-100)", false},
	{"contrast", "range", "Contrast (0-100)", false},
	{"sharpness", "range", "Sharpness", false},
	{"bluelight", "range", "Hardware blue light filter", true},
	{"colortemp", "enum", "Color temperature preset", false},
	{"preset", "enum", "Picture preset (ProArt/Splendid)", false},
	{"input", "enum", "Input source", false},
	{"kvm", "enum", "KVM / USB upstream", false},
}

func init() {
	for _, fc := range featureCmds {
		name := fc.name
		get := func(*cobra.Command, []string) error { return getFeature(name) }
		parent := &cobra.Command{Use: name, Short: fc.short, Args: cobra.NoArgs, RunE: get}
		parent.AddCommand(&cobra.Command{Use: "get", Short: "Show the current value", Args: cobra.NoArgs, RunE: get})
		setUse := "set <value|+N|-N>"
		if fc.kind == "enum" {
			setUse = "set <name>"
			parent.AddCommand(&cobra.Command{Use: "list", Short: "List the values this monitor supports", Args: cobra.NoArgs,
				RunE: func(*cobra.Command, []string) error { return listValues(name) }})
		}
		parent.AddCommand(&cobra.Command{Use: setUse, Short: "Set the value", Args: cobra.ExactArgs(1),
			RunE: func(_ *cobra.Command, a []string) error { return setFeature(name, a[0]) }})
		if fc.onOff {
			for _, v := range []string{"on", "off", "toggle"} {
				parent.AddCommand(&cobra.Command{Use: v, Short: "Turn " + name + " " + v, Args: cobra.NoArgs,
					RunE: func(*cobra.Command, []string) error { return setFeature(name, v) }})
			}
		}
		rootCmd.AddCommand(parent)
	}
}

func getFeature(name string) error {
	var fv deck.FeatureValue
	if err := call("get", deck.Params{Feature: name}, &fv); err != nil {
		return err
	}
	return printValue(fv)
}

func setFeature(name, value string) error {
	raw, _ := json.Marshal(value)
	var fv deck.FeatureValue
	if err := call("set", deck.Params{Feature: name, Value: raw}, &fv); err != nil {
		return err
	}
	if quietFlag {
		return nil
	}
	return printValue(fv)
}

func printValue(fv deck.FeatureValue) error {
	if jsonFlag {
		return printJSON(fv)
	}
	fmt.Println(formatValue(fv))
	return nil
}

func formatValue(fv deck.FeatureValue) string {
	switch {
	case fv.Name != "":
		return fv.Name
	case fv.On != nil:
		return fmt.Sprintf("%s (%d/%d)", map[bool]string{true: "on", false: "off"}[*fv.On], fv.Value, fv.Max)
	case fv.Max > 0:
		return fmt.Sprintf("%d/%d", fv.Value, fv.Max)
	}
	return fmt.Sprintf("0x%02x", fv.Value)
}

func listValues(name string) error {
	var m deck.Monitor
	if err := call("monitor", deck.Params{}, &m); err != nil {
		return err
	}
	f, ok := m.Features[name]
	if !ok {
		return &deck.Error{Code: deck.ErrUnsupported, Message: fmt.Sprintf("%s does not support %s", m.ID, name)}
	}
	if jsonFlag {
		return printJSON(f.Values)
	}
	names := make([]string, 0, len(f.Values))
	for k := range f.Values {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		fmt.Printf("%-10s 0x%02x\n", k, f.Values[k])
	}
	return nil
}
