package cmd

import (
	"github.com/dietermayrhofer/dtingest/pkg/installer"
	"github.com/spf13/cobra"
)

var uninstallCmd = &cobra.Command{
	Use:   "uninstall <method>",
	Short: "Uninstall a Dynatrace ingestion method",
}

var uninstallKubernetesCmd = &cobra.Command{
	Use:   "kubernetes",
	Short: "Remove Dynatrace Operator and DynaKube resources from Kubernetes",
	RunE: func(cmd *cobra.Command, args []string) error {
		return installer.UninstallKubernetes()
	},
}

var uninstallOneAgentCmd = &cobra.Command{
	Use:   "oneagent",
	Short: "Uninstall Dynatrace OneAgent from this host",
	RunE: func(cmd *cobra.Command, args []string) error {
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		return installer.UninstallOneAgent(dryRun)
	},
}

func init() {
	uninstallOneAgentCmd.Flags().Bool("dry-run", false, "Show what would be done without making changes")
	uninstallCmd.AddCommand(uninstallKubernetesCmd)
	uninstallCmd.AddCommand(uninstallOneAgentCmd)
}
