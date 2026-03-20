package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the dtwiz version",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("dtwiz %s\n", Version)
	},
}
