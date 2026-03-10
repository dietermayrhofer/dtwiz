package installer

import (
	"fmt"
	"os"
	"runtime"

	"github.com/fatih/color"
)

const (
	// Linux: standard uninstall script path.
	linuxUninstallScript = "/opt/dynatrace/oneagent/agent/uninstall.sh"

	// Windows: standard uninstall command path.
	// Uses %ProgramData% which is typically C:\ProgramData.
	windowsUninstallCmd = `C:\ProgramData\dynatrace\oneagent\agent\uninstall.cmd`
)

// UninstallOneAgent removes Dynatrace OneAgent from the current host.
func UninstallOneAgent(dryRun bool) error {
	switch runtime.GOOS {
	case "linux":
		return uninstallOneAgentLinux(dryRun)
	case "windows":
		return uninstallOneAgentWindows(dryRun)
	case "darwin":
		return fmt.Errorf("OneAgent is not supported on macOS — nothing to uninstall")
	default:
		return fmt.Errorf("unsupported operating system: %s", runtime.GOOS)
	}
}

func uninstallOneAgentLinux(dryRun bool) error {
	// Verify the uninstall script exists.
	if _, err := os.Stat(linuxUninstallScript); os.IsNotExist(err) {
		return fmt.Errorf("OneAgent uninstall script not found at %s — is OneAgent installed?", linuxUninstallScript)
	}

	header := color.New(color.FgCyan, color.Bold)
	muted := color.New(color.FgHiBlack)

	header.Println("  OneAgent Uninstall (Linux)")
	muted.Println("  " + "────────────────────────────────────────")
	fmt.Println()
	fmt.Printf("  Uninstall script:  %s\n", linuxUninstallScript)

	if needsSudo() {
		fmt.Println("  Privileges:        sudo required (current user is not root)")
	}
	fmt.Println()

	if dryRun {
		fmt.Println("[dry-run] Would run the OneAgent uninstall script. No changes made.")
		return nil
	}

	ok, err := confirmProceed("  Proceed with OneAgent uninstall?")
	if err != nil {
		return fmt.Errorf("reading confirmation: %w", err)
	}
	if !ok {
		fmt.Println("  Uninstall cancelled.")
		return nil
	}
	fmt.Println()

	// Run the uninstall script, prepending sudo if needed.
	args := []string{linuxUninstallScript}
	if needsSudo() {
		args = append([]string{"sudo"}, args...)
	}

	fmt.Println("  Running OneAgent uninstall script...")
	if err := RunCommand(args[0], args[1:]...); err != nil {
		return fmt.Errorf("OneAgent uninstall failed: %w", err)
	}

	color.New(color.FgGreen, color.Bold).Println("\n  OneAgent uninstalled successfully.")
	return nil
}

func uninstallOneAgentWindows(dryRun bool) error {
	// Resolve the actual path using %ProgramData%.
	programData := os.Getenv("ProgramData")
	uninstallPath := windowsUninstallCmd
	if programData != "" {
		uninstallPath = programData + `\dynatrace\oneagent\agent\uninstall.cmd`
	}

	// Verify the uninstall command exists.
	if _, err := os.Stat(uninstallPath); os.IsNotExist(err) {
		return fmt.Errorf("OneAgent uninstall script not found at %s — is OneAgent installed?", uninstallPath)
	}

	header := color.New(color.FgCyan, color.Bold)
	muted := color.New(color.FgHiBlack)

	header.Println("  OneAgent Uninstall (Windows)")
	muted.Println("  " + "────────────────────────────────────────")
	fmt.Println()
	fmt.Printf("  Uninstall command:  %s\n", uninstallPath)
	fmt.Println()

	if dryRun {
		fmt.Println("[dry-run] Would run the OneAgent uninstall command. No changes made.")
		return nil
	}

	ok, err := confirmProceed("  Proceed with OneAgent uninstall?")
	if err != nil {
		return fmt.Errorf("reading confirmation: %w", err)
	}
	if !ok {
		fmt.Println("  Uninstall cancelled.")
		return nil
	}
	fmt.Println()

	fmt.Println("  Running OneAgent uninstall command...")
	if err := RunCommand("cmd.exe", "/C", uninstallPath); err != nil {
		return fmt.Errorf("OneAgent uninstall failed: %w", err)
	}

	color.New(color.FgGreen, color.Bold).Println("\n  OneAgent uninstalled successfully.")
	return nil
}
