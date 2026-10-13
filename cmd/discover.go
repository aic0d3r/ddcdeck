package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/aic0d3r/ddcdeck/internal/ddc"
	"github.com/aic0d3r/ddcdeck/internal/deck"
	"github.com/aic0d3r/ddcdeck/internal/model"
	"github.com/aic0d3r/ddcdeck/internal/wizard"
)

var discoverFeature string

func init() {
	c := &cobra.Command{
		Use:   "discover",
		Short: "Guided discovery of vendor VCP codes by diffing OSD changes",
		Args:  cobra.NoArgs,
		RunE:  runDiscover,
	}
	c.Flags().StringVar(&discoverFeature, "feature", "", "feature name to record (e.g. bluelight)")
	rootCmd.AddCommand(c)
}

var nameOK = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func runDiscover(*cobra.Command, []string) error {
	if fi, err := os.Stdin.Stat(); err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return errors.New("discover is interactive: run it in a terminal")
	}
	in := bufio.NewReader(os.Stdin)
	var m deck.Monitor
	if err := call("monitor", deck.Params{}, &m); err != nil {
		return err
	}
	sess, err := wizard.Start(ddc.Ddcutil{}, m.Bus)
	if err != nil {
		return &deck.Error{Code: deck.ErrDDC, Message: err.Error()}
	}
	fmt.Printf("Watching %d VCP codes on %s (%s).\n", len(sess.Codes()), m.ID, m.Connector)
	for {
		name := discoverFeature
		if name == "" {
			name = prompt(in, "Feature to discover (e.g. bluelight, kvm, preset)")
		}
		if !nameOK.MatchString(name) {
			fmt.Println("Names are lowercase letters, digits and underscores. Skipping.")
			if discoverFeature != "" {
				return nil
			}
			continue
		}
		feat, err := discoverOne(in, sess, name)
		if err != nil {
			fmt.Println(err)
		}
		if feat != nil {
			path, mccs, err := saveDiscovery(m, name, *feat)
			if err != nil {
				fmt.Println("save failed:", err)
			} else {
				fmt.Printf("Saved %s\n  model file:  %s\n  ddcutil file: %s\n", name, path, mccs)
				fmt.Printf("Try it: ddcdeck %s get   (restart ddcdeckd if it's running)\n", name)
				fmt.Println("Submit it: copy the model file into data/models/ and open a PR.")
			}
		}
		if discoverFeature != "" || !yes(in, "Discover another feature?") {
			return nil
		}
	}
}

// discoverOne runs one feature through snapshot → OSD change → diff → confirm.
func discoverOne(in *bufio.Reader, sess *wizard.Session, name string) (*model.Feature, error) {
	prev := sess.Base()
	var diff map[byte][2]ddc.Value
	for round := 1; round <= wizard.MaxRounds; round++ {
		if round == 1 {
			fmt.Printf("In the monitor's OSD, change %q now, then press Enter.", name)
		} else {
			fmt.Print("Set it to a different value, then press Enter.")
		}
		readLine(in)
		snap, err := sess.Snapshot()
		if err != nil {
			return nil, err
		}
		d := wizard.Changed(prev, snap) // consecutive diff: only this round's change appears
		if round > 1 {
			d = wizard.Intersect(diff, d)
			for c, v := range d { // keep the very first old value for the write-back check
				d[c] = [2]ddc.Value{diff[c][0], v[1]}
			}
		}
		prev, diff = snap, d
		switch len(diff) {
		case 1:
			return confirm(in, sess, name, diff)
		case 0:
			fmt.Print("No VCP code changed. Make sure you apply the setting in the OSD.")
			if !yes(in, "Try again?") {
				return nil, errors.New("aborted")
			}
			round--
		default:
			fmt.Printf("%d codes changed (%s) — ambiguous. Set the feature to a different value to narrow it down.",
				len(diff), strings.Join(codesOf(diff), " "))
		}
	}
	return nil, errors.New("could not narrow it down; the OSD change touches several codes")
}

func codesOf(d map[byte][2]ddc.Value) []string {
	out := make([]string, 0, len(d))
	for c := range d {
		out = append(out, fmt.Sprintf("%02X", c))
	}
	return out
}

// confirm asks for the feature shape, then reverts the value as a write-back check.
func confirm(in *bufio.Reader, sess *wizard.Session, name string, diff map[byte][2]ddc.Value) (*model.Feature, error) {
	var code byte
	var pair [2]ddc.Value
	for c, v := range diff {
		code, pair = c, v
	}
	fmt.Printf("Found it: VCP %02X changed %d → %d.\n", code, pair[0].Cur, pair[1].Cur)
	finding := wizard.Finding{Code: code, Old: pair[0], New: pair[1]}

	var off *int
	values := map[string]int{}
	if finding.New.NC {
		fmt.Println("This looks like a list of named settings.")
		cur := finding.New.Cur
		for {
			v := prompt(in, fmt.Sprintf("Name the value at 0x%02X (e.g. hdmi1; blank to finish)", cur))
			if v == "" {
				break
			}
			values[v] = cur
			fmt.Printf("Set %q to another setting in the OSD, then press Enter (or just Enter to finish).", name)
			if strings.TrimSpace(readLine(in)) == "" {
				break
			}
			snap, err := sess.Snapshot()
			if err != nil {
				return nil, err
			}
			cur = snap[code].Cur
		}
		if len(values) == 0 {
			return nil, errors.New("need at least one named value")
		}
	} else if o := prompt(in, fmt.Sprintf("Range feature (max %d). Which value means off? (blank if none)", finding.New.Max)); o != "" {
		n, err := strconv.Atoi(o)
		if err != nil {
			return nil, errors.New("off must be a number")
		}
		off = &n
	}

	fmt.Printf("Writing the old value (%d) back through DDC…", finding.Old.Cur)
	if err := sess.Set(code, finding.Old.Cur); err != nil {
		return nil, err
	}
	fmt.Printf(" done. Did the OSD show %q revert?", name)
	if !yes(in, "") {
		return nil, errors.New("write-back check failed; not recording this code")
	}
	f := finding.Feature(off, values)
	return &f, nil
}

func saveDiscovery(m deck.Monitor, name string, f model.Feature) (modelPath, mccsPath string, err error) {
	disco := model.Model{Mfg: m.Mfg, Model: m.Model, Product: m.Product,
		Features: map[string]model.Feature{name: f}}
	modelPath, err = model.SaveUserModel(disco)
	if err != nil {
		return "", "", err
	}
	if err = os.MkdirAll(model.DdcutilDir(), 0o700); err != nil {
		return modelPath, "", err
	}
	mccsPath = filepath.Join(model.DdcutilDir(), idOf(m)+".mccs")
	return modelPath, mccsPath, os.WriteFile(mccsPath, []byte(disco.MCCS()), 0o600)
}

func idOf(m deck.Monitor) string { return fmt.Sprintf("%s-%s-%d", m.Mfg, m.Model, m.Product) }

func prompt(in *bufio.Reader, q string) string {
	fmt.Printf("%s: ", q)
	return strings.ToLower(strings.TrimSpace(readLine(in)))
}

func readLine(in *bufio.Reader) string {
	line, err := in.ReadString('\n')
	if err != nil && line == "" {
		return ""
	}
	return strings.TrimRight(line, "\r\n")
}

func yes(in *bufio.Reader, q string) bool {
	fmt.Printf("%s [yN]: ", strings.TrimSpace(q))
	return strings.HasPrefix(strings.ToLower(readLine(in)), "y")
}
