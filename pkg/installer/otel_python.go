package installer

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/fatih/color"
)

// OtelPythonResult holds the outcome of an OTel Python auto-instrumentation setup.
type OtelPythonResult struct {
	PythonPath    string
	PipPath       string
	VirtualEnv    string
	PackagesAdded []string
	EnvVars       map[string]string
}

// PythonProcess describes a detected running Python process.
type PythonProcess struct {
	PID     int
	Command string // full command line
}

// detectPython finds a usable Python 3 executable on the current PATH,
// preferring python3 over python.
func detectPython() (string, error) {
	for _, name := range []string{"python3", "python"} {
		path, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		// Verify it's actually Python 3.
		out, err := exec.Command(path, "--version").Output()
		if err != nil {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(string(out)), "Python 3") {
			return path, nil
		}
	}
	return "", fmt.Errorf("Python 3 not found — install Python 3 and ensure it is in PATH")
}

// pipCommand holds the resolved pip executable and arguments.
type pipCommand struct {
	name string
	args []string
}

// detectPip finds a usable pip for the given Python interpreter.
func detectPip(pythonPath string) (*pipCommand, error) {
	// Try pip3 / pip first.
	for _, name := range []string{"pip3", "pip"} {
		if path, err := exec.LookPath(name); err == nil {
			return &pipCommand{name: path}, nil
		}
	}
	// Fall back to `python -m pip`.
	if err := exec.Command(pythonPath, "-m", "pip", "--version").Run(); err == nil {
		return &pipCommand{name: pythonPath, args: []string{"-m", "pip"}}, nil
	}
	return nil, fmt.Errorf("pip not found — install pip for Python 3")
}

// detectVirtualEnv returns the current virtual environment path, or empty
// string if none is active.
func detectVirtualEnv() string {
	return os.Getenv("VIRTUAL_ENV")
}

// isPackageInstalled checks whether a Python package is importable.
func isPackageInstalled(pythonPath, packageName string) bool {
	return exec.Command(pythonPath, "-c", fmt.Sprintf("import %s", packageName)).Run() == nil
}

// otelPythonPackages is the list of OpenTelemetry packages to install for
// auto-instrumentation.
var otelPythonPackages = []string{
	"opentelemetry-api",
	"opentelemetry-sdk",
	"opentelemetry-exporter-otlp",
	"opentelemetry-instrumentation",
}

// installPackages installs the given pip packages using the resolved pip command.
func installPackages(pip *pipCommand, packages []string) error {
	args := append(append([]string{}, pip.args...), append([]string{"install"}, packages...)...)
	cmd := exec.Command(pip.name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pip install failed: %w", err)
	}
	return nil
}

// generateOtelPythonEnvVars returns the OTEL_* environment variables required
// for auto-instrumentation to export to Dynatrace.
func generateOtelPythonEnvVars(apiURL, token, serviceName string) map[string]string {
	return map[string]string{
		"OTEL_SERVICE_NAME":            serviceName,
		"OTEL_EXPORTER_OTLP_ENDPOINT": strings.TrimRight(apiURL, "/") + "/api/v2/otlp",
		"OTEL_EXPORTER_OTLP_HEADERS":  "Authorization=Api-Token " + token,
		"OTEL_TRACES_EXPORTER":        "otlp",
		"OTEL_METRICS_EXPORTER":       "otlp",
		"OTEL_LOGS_EXPORTER":          "otlp",
		"OTEL_PYTHON_LOGGING_AUTO_INSTRUMENTATION_ENABLED": "true",
	}
}

// GenerateEnvExportScript returns a shell `export` script for the given env vars.
func GenerateEnvExportScript(envVars map[string]string) string {
	var sb strings.Builder
	sb.WriteString("# Dynatrace OpenTelemetry auto-instrumentation environment variables\n")
	for k, v := range envVars {
		sb.WriteString(fmt.Sprintf("export %s=%q\n", k, v))
	}
	return sb.String()
}

// detectPythonProcesses finds running Python processes (excluding the current
// process and common system Python processes).
func detectPythonProcesses() []PythonProcess {
	// Use ps to find python processes with full command line.
	out, err := exec.Command("ps", "ax", "-o", "pid=,command=").Output()
	if err != nil {
		return nil
	}

	var procs []PythonProcess
	myPID := os.Getpid()
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Split into PID and command.
		parts := strings.SplitN(line, " ", 2)
		if len(parts) < 2 {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(parts[0]))
		if err != nil || pid == myPID {
			continue
		}
		cmd := strings.TrimSpace(parts[1])
		// Match python processes but skip system/pip/setup processes.
		if !strings.Contains(cmd, "python") {
			continue
		}
		if strings.Contains(cmd, "pip ") || strings.Contains(cmd, "setup.py") ||
			strings.Contains(cmd, "/bin/dtwiz") || strings.Contains(cmd, "opentelemetry-instrument") {
			continue
		}
		procs = append(procs, PythonProcess{PID: pid, Command: cmd})
	}
	return procs
}

// promptPythonRestart shows detected Python processes and offers to restart
// them with the OTel environment variables.
func promptPythonRestart(procs []PythonProcess, envVars map[string]string) {
	if len(procs) == 0 {
		return
	}

	otelHeader := color.New(color.FgMagenta, color.Bold)
	otelMuted := color.New()

	fmt.Println()
	otelHeader.Println("  Detected Python processes:")
	otelMuted.Println("  " + strings.Repeat("─", 42))
	for i, p := range procs {
		fmt.Printf("  [%d]  PID %-6d %s\n", i+1, p.PID, truncateCmd(p.Command, 60))
	}
	fmt.Println()
	fmt.Println("  To instrument these processes, restart them with the env vars above.")
	fmt.Println("  Example:")
	fmt.Println("    opentelemetry-instrument python your_app.py")
	fmt.Println()
	fmt.Print("  Restart selected processes now? [y/N]: ")

	reader := bufio.NewReader(os.Stdin)
	answer, _ := reader.ReadString('\n')
	answer = strings.TrimSpace(strings.ToLower(answer))
	if answer != "y" && answer != "yes" {
		return
	}

	fmt.Println()
	for _, p := range procs {
		fmt.Printf("  Sending SIGTERM to PID %d (%s)...\n", p.PID, truncateCmd(p.Command, 40))
		proc, err := os.FindProcess(p.PID)
		if err != nil {
			fmt.Printf("    Warning: could not find process %d: %v\n", p.PID, err)
			continue
		}
		if err := proc.Signal(os.Interrupt); err != nil {
			fmt.Printf("    Warning: could not signal process %d: %v\n", p.PID, err)
		} else {
			fmt.Printf("    Stopped PID %d — restart it manually with the OTel env vars.\n", p.PID)
		}
	}
}

// truncateCmd shortens a command string for display.
func truncateCmd(cmd string, maxLen int) string {
	if len(cmd) <= maxLen {
		return cmd
	}
	return cmd[:maxLen-3] + "..."
}

// InstallOtelPython sets up OpenTelemetry auto-instrumentation for Python
// applications and prints the required environment variables.
//
// Parameters:
//   - envURL:       Dynatrace environment URL
//   - token:        API token (Ingest scope)
//   - serviceName:  OTEL_SERVICE_NAME value (defaults to "my-service" if empty)
//   - dryRun:       when true, only print what would be done
func InstallOtelPython(envURL, token, serviceName string, dryRun bool) error {
	apiURL := APIURL(envURL)

	if serviceName == "" {
		serviceName = "my-service"
	}

	envVars := generateOtelPythonEnvVars(apiURL, token, serviceName)

	if dryRun {
		fmt.Println("[dry-run] Would set up OpenTelemetry Python auto-instrumentation")
		fmt.Printf("  API URL:      %s\n", apiURL)
		fmt.Printf("  Service name: %s\n", serviceName)
		fmt.Println("  Packages to install:")
		for _, pkg := range otelPythonPackages {
			fmt.Printf("    - %s\n", pkg)
		}
		fmt.Println()
		fmt.Println("  Environment variables to set:")
		for k, v := range envVars {
			fmt.Printf("    %s=%s\n", k, v)
		}
		return nil
	}

	// 1. Detect Python.
	pythonPath, err := detectPython()
	if err != nil {
		return err
	}
	fmt.Printf("  Python: %s\n", pythonPath)

	// 2. Detect pip.
	pip, err := detectPip(pythonPath)
	if err != nil {
		return err
	}
	fmt.Printf("  pip:    %s\n", pip.name)

	// 3. Warn if not in a virtualenv.
	if venv := detectVirtualEnv(); venv != "" {
		fmt.Printf("  Virtual env: %s\n", venv)
	} else {
		fmt.Println("  WARNING: No virtual environment detected — packages will be installed globally")
	}

	// 4. Install packages.
	fmt.Println("  Installing OpenTelemetry packages...")
	if err := installPackages(pip, otelPythonPackages); err != nil {
		return err
	}

	// 5. Print env var export script.
	fmt.Println()
	fmt.Println("  Installation complete. Add the following to your environment:")
	fmt.Println()
	fmt.Println(GenerateEnvExportScript(envVars))
	fmt.Println("  Then run your application with:")
	fmt.Printf("    opentelemetry-instrument python your_app.py\n")

	// 6. Detect running Python processes and offer restart.
	procs := detectPythonProcesses()
	promptPythonRestart(procs, envVars)

	return nil
}
