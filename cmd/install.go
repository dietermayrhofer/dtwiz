package cmd

import (
	"github.com/dietermayrhofer/dtwiz/pkg/installer"
	"github.com/spf13/cobra"
)

var installDryRun bool

var installCmd = &cobra.Command{
	Use:   "install <method>",
	Short: "Install a Dynatrace ingestion method",
}

var installOneAgentCmd = &cobra.Command{
	Use:   "oneagent",
	Short: "Install Dynatrace OneAgent on this host",
	RunE: func(cmd *cobra.Command, args []string) error {
		envURL, token, err := getDtEnvironment()
		if err != nil {
			return err
		}
		quiet, _ := cmd.Flags().GetBool("quiet")
		hostGroup, _ := cmd.Flags().GetString("host-group")
		return installer.InstallOneAgent(envURL, token, installDryRun, quiet, hostGroup)
	},
}

var installKubernetesCmd = &cobra.Command{
	Use:   "kubernetes",
	Short: "Deploy Dynatrace Operator on Kubernetes",
	RunE: func(cmd *cobra.Command, args []string) error {
		envURL, token, err := getDtEnvironment()
		if err != nil {
			return err
		}
		return installer.InstallKubernetes(envURL, token, accessToken(), "", installDryRun)
	},
}

var installDockerCmd = &cobra.Command{
	Use:   "docker",
	Short: "Install Dynatrace OneAgent for Docker",
	RunE: func(cmd *cobra.Command, args []string) error {
		envURL, token, err := getDtEnvironment()
		if err != nil {
			return err
		}
		return installer.InstallDocker(envURL, token, installDryRun)
	},
}

var installOtelCmd = &cobra.Command{
	Use:   "otel-collector",
	Short: "Install or configure OpenTelemetry Collector",
	RunE: func(cmd *cobra.Command, args []string) error {
		envURL, token, err := getDtEnvironment()
		if err != nil {
			return err
		}
		return installer.InstallOtelCollector(envURL, token, accessToken(), platformToken(), installDryRun)
	},
}

var otelUpdateConfigPath string
var installOtelUpdateCmd = &cobra.Command{
	Use:   "otel-update",
	Short: "Patch an existing OTel Collector config with the Dynatrace exporter",
	RunE: func(cmd *cobra.Command, args []string) error {
		envURL, token, err := getDtEnvironment()
		if err != nil {
			return err
		}
		return installer.UpdateOtelConfig(otelUpdateConfigPath, envURL, token, platformToken(), installDryRun)
	},
}

var otelPythonServiceName string
var installOtelPythonCmd = &cobra.Command{
	Use:   "otel-python",
	Short: "Set up OpenTelemetry Python auto-instrumentation",
	RunE: func(cmd *cobra.Command, args []string) error {
		envURL, token, err := getDtEnvironment()
		if err != nil {
			return err
		}
		return installer.InstallOtelPython(envURL, token, otelPythonServiceName, installDryRun)
	},
}

var installAWSCmd = &cobra.Command{
	Use:   "aws",
	Short: "Set up Dynatrace AWS CloudFormation integration",
	RunE: func(cmd *cobra.Command, args []string) error {
		envURL, token, err := getDtEnvironment()
		if err != nil {
			return err
		}
		return installer.InstallAWS(envURL, token, platformToken(), installDryRun)
	},
}

func init() {
	installCmd.PersistentFlags().BoolVar(&installDryRun, "dry-run", false, "show what would be done without executing")

	installOtelUpdateCmd.Flags().StringVar(&otelUpdateConfigPath, "config", "config.yaml", "path to the existing OTel Collector config file to patch")
	installOtelPythonCmd.Flags().StringVar(&otelPythonServiceName, "service-name", "", "OTEL_SERVICE_NAME for the instrumented application (default: my-service)")

	installOneAgentCmd.Flags().Bool("quiet", false, "Run a silent/unattended installation with no output")
	installOneAgentCmd.Flags().String("host-group", "", "Assign the host to a host group (--set-host-group)")
	installCmd.AddCommand(installOneAgentCmd)
	installCmd.AddCommand(installKubernetesCmd)
	installCmd.AddCommand(installDockerCmd)
	installCmd.AddCommand(installOtelCmd)
	installCmd.AddCommand(installOtelUpdateCmd)
	installCmd.AddCommand(installOtelPythonCmd)
	installCmd.AddCommand(installAWSCmd)
}
