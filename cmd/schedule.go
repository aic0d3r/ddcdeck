package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/aic0d3r/ddcdeck/internal/automation"
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
	if err := automation.SetBlueLightSchedule(spec, scheduleLevel); err != nil {
		return err
	}
	if !quietFlag {
		if spec == "off" {
			fmt.Println("blue light schedule removed")
		} else {
			fmt.Printf("blue light on at %s (level %d), off rules follow; reloads within 20 s\n", spec, scheduleLevel)
		}
	}
	return nil
}
