package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var scheduleLevel int

func init() {
	bl, _, err := rootCmd.Find([]string{"bluelight"})
	if err != nil {
		return
	}
	schedule := &cobra.Command{
		Use:   "schedule <sunset|off|HH:MM-HH:MM>",
		Short: "Blue light on/off by sun or clock, replacing any previous schedule",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, a []string) error {
			return setSchedule(a[0])
		},
	}
	schedule.Flags().IntVar(&scheduleLevel, "level", 20, "filter strength when on")
	bl.AddCommand(schedule)
}

func setSchedule(spec string) error {
	if err := call("schedule.set", map[string]any{"spec": spec, "level": scheduleLevel}, nil); err != nil {
		return err
	}
	if !quietFlag {
		if spec == "off" {
			fmt.Println("blue light schedule off")
		} else {
			fmt.Printf("blue light follows %s (level %d), applied now\n", spec, scheduleLevel)
		}
	}
	return nil
}
