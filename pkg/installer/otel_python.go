package installer

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

// PythonProject describes a detected Python project directory.
type PythonProject struct {
	Path    string // absolute path to the project directory
	Markers []string // which indicator files were found (e.g. "pyproject.toml", "requirements.txt")
}

// pythonProjectMarkers are the files that indicate a Python project root.
var pythonProjectMarkers = []string{
	"pyproject.toml",
	"setup.py",
	"setup.cfg",
	"requirements.txt",
	"Pipfile",
	"poetry.lock",
	"manage.py",
}

// detectPythonProjects scans common locations for Python project directories.
// Looks in the current working directory and one level of subdirectories, plus
// common project locations under $HOME.
func detectPythonProjects() []PythonProject {
	var projects []PythonProject
	seen := make(map[string]bool)

	checkDir := func(dir string) {
		// Resolve symlinks and normalize to lowercase for case-insensitive
		// filesystems (macOS APFS).
		resolved, err := filepath.EvalSymlinks(dir)
		if err != nil {
			resolved = dir
		}
		key := strings.ToLower(resolved)
		if seen[key] {
			return
		}
		seen[key] = true
		var markers []string
		for _, marker := range pythonProjectMarkers {
			if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
				markers = append(markers, marker)
			}
		}
		if len(markers) > 0 {
			projects = append(projects, PythonProject{Path: dir, Markers: markers})
		}
	}

	// Check CWD and immediate subdirectories.
	if cwd, err := os.Getwd(); err == nil {
		checkDir(cwd)
		entries, _ := os.ReadDir(cwd)
		for _, e := range entries {
			if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
				checkDir(filepath.Join(cwd, e.Name()))
			}
		}
	}

	// Check common home-directory project locations (two levels deep).
	if home, err := os.UserHomeDir(); err == nil {
		for _, base := range []string{"projects", "code", "src", "dev", "Code"} {
			dir := filepath.Join(home, base)
			entries, err := os.ReadDir(dir)
			if err != nil {
				continue
			}
			for _, e := range entries {
				if !e.IsDir() {
					continue
				}
				sub := filepath.Join(dir, e.Name())
				checkDir(sub)
				// Also check one level deeper (e.g. ~/Code/data-generators/orderschnitzel).
				subEntries, err := os.ReadDir(sub)
				if err != nil {
					continue
				}
				for _, se := range subEntries {
					if se.IsDir() && !strings.HasPrefix(se.Name(), ".") {
						checkDir(filepath.Join(sub, se.Name()))
					}
				}
			}
		}
	}

	return projects
}

// promptPythonInstrumentation shows detected Python processes and projects,
// and offers to act on them.
func promptPythonInstrumentation(procs []PythonProcess, projects []PythonProject, envVars map[string]string) {
	if len(procs) == 0 && len(projects) == 0 {
		return
	}

	otelHeader := color.New(color.FgMagenta, color.Bold)
	otelMuted := color.New()

	// Track numbering across both sections.
	idx := 0

	if len(procs) > 0 {
		fmt.Println()
		otelHeader.Println("  Running Python processes:")
		otelMuted.Println("  " + strings.Repeat("─", 50))
		for _, p := range procs {
			idx++
			fmt.Printf("  [%d]  PID %-6d %s\n", idx, p.PID, truncateCmd(p.Command, 60))
		}
	}

	if len(projects) > 0 {
		fmt.Println()
		otelHeader.Println("  Python projects on this machine:")
		otelMuted.Println("  " + strings.Repeat("─", 50))
		for _, proj := range projects {
			idx++
			fmt.Printf("  [%d]  %s  (%s)\n", idx, proj.Path, strings.Join(proj.Markers, ", "))
		}
	}

	totalItems := len(procs) + len(projects)
	fmt.Println()

	fmt.Printf("  Select an item to instrument [1-%d] or press Enter to skip: ", totalItems)

	reader := bufio.NewReader(os.Stdin)
	answer, _ := reader.ReadString('\n')
	answer = strings.TrimSpace(strings.ToLower(answer))
	if answer == "" || answer == "n" || answer == "no" {
		return
	}

	num, err := strconv.Atoi(answer)
	if err != nil || num < 1 || num > totalItems {
		fmt.Println("  Invalid selection, skipping.")
		return
	}

	if num <= len(procs) {
		// Selected a running process → stop it and restart with OTel instrumentation.
		p := procs[num-1]
		fmt.Printf("  Stopping PID %d...\n", p.PID)
		proc, err := os.FindProcess(p.PID)
		if err != nil {
			fmt.Printf("    Warning: could not find process %d: %v\n", p.PID, err)
			return
		}
		if err := proc.Signal(os.Interrupt); err != nil {
			fmt.Printf("    Warning: could not signal process %d: %v\n", p.PID, err)
			return
		}
		fmt.Printf("    Stopped PID %d.\n", p.PID)

		// Restart the process wrapped with opentelemetry-instrument.
		fmt.Println("  Restarting with OpenTelemetry instrumentation...")
		restartArgs := buildInstrumentedCommand(p.Command)
		cmd := exec.Command(restartArgs[0], restartArgs[1:]...)
		cmd.Env = append(os.Environ(), envVarsToSlice(envVars)...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			fmt.Printf("    Failed to restart: %v\n", err)
			return
		}
		fmt.Printf("    Started instrumented process (PID %d).\n", cmd.Process.Pid)
	} else {
		// Selected a project → install OTel packages in its virtualenv.
		proj := projects[num-len(procs)-1]
		fmt.Printf("\n  Instrumenting project: %s\n", proj.Path)
		instrumentProject(proj, envVars)
	}
}

// buildInstrumentedCommand takes an original python command line and wraps it
// with opentelemetry-instrument, preserving the original arguments.
func buildInstrumentedCommand(cmdLine string) []string {
	parts := strings.Fields(cmdLine)
	if len(parts) == 0 {
		return []string{"opentelemetry-instrument", "python"}
	}
	// Find the python binary and everything after it.
	// e.g. "/usr/bin/python3 app.py --port 8080" → "opentelemetry-instrument python3 app.py --port 8080"
	return append([]string{"opentelemetry-instrument"}, parts...)
}

// envVarsToSlice converts an env var map to KEY=VALUE slice for exec.Cmd.Env.
func envVarsToSlice(envVars map[string]string) []string {
	out := make([]string, 0, len(envVars))
	for k, v := range envVars {
		out = append(out, k+"="+v)
	}
	return out
}

// instrumentProject installs OTel packages into a Python project's environment.
func instrumentProject(proj PythonProject, envVars map[string]string) {
	// Detect virtualenv inside the project.
	venvPip := detectProjectPip(proj.Path)
	if venvPip == nil {
		fmt.Println("  No virtualenv found in project. Creating one...")
		pythonPath, err := detectPython()
		if err != nil {
			fmt.Printf("    %v\n", err)
			return
		}
		venvDir := filepath.Join(proj.Path, ".venv")
		cmd := exec.Command(pythonPath, "-m", "venv", venvDir)
		cmd.Dir = proj.Path
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Printf("    Failed to create virtualenv: %v\n", err)
			return
		}
		fmt.Printf("    Created virtualenv: %s\n", venvDir)
		venvPip = detectProjectPip(proj.Path)
		if venvPip == nil {
			fmt.Println("    Could not find pip in new virtualenv.")
			return
		}
	}

	fmt.Printf("  Installing OTel packages with: %s\n", venvPip.name)
	if err := installPackages(venvPip, otelPythonPackages); err != nil {
		fmt.Printf("    %v\n", err)
		return
	}
	fmt.Println("  Packages installed.")

	// Find entrypoints (may be multiple, e.g. microservice subfolders).
	entrypoints := detectPythonEntrypoints(proj.Path)

	if len(entrypoints) == 0 {
		fmt.Println()
		fmt.Print("  No entrypoint detected. Enter the Python file to run (e.g. app.py): ")
		reader := bufio.NewReader(os.Stdin)
		answer, _ := reader.ReadString('\n')
		ep := strings.TrimSpace(answer)
		if ep == "" {
			fmt.Println("  No entrypoint provided, skipping.")
			return
		}
		entrypoints = []string{ep}
	} else if len(entrypoints) == 1 {
		fmt.Printf("  Detected entrypoint: %s\n", entrypoints[0])
	} else {
		fmt.Println()
		fmt.Printf("  Detected %d entrypoints:\n", len(entrypoints))
		for i, ep := range entrypoints {
			fmt.Printf("    [%d] %s\n", i+1, ep)
		}
		fmt.Printf("  Run all, or select one? [Y/1-%d/n]: ", len(entrypoints))
		reader := bufio.NewReader(os.Stdin)
		answer, _ := reader.ReadString('\n')
		answer = strings.TrimSpace(strings.ToLower(answer))
		if answer == "n" || answer == "no" {
			return
		}
		if num, err := strconv.Atoi(answer); err == nil && num >= 1 && num <= len(entrypoints) {
			entrypoints = []string{entrypoints[num-1]}
		}
		// else: run all (Enter/Y)
	}

	// Resolve binaries from the project's venv.
	otelInstrument := resolveVenvBinary(proj.Path, "opentelemetry-instrument")
	pythonBin := resolveVenvBinary(proj.Path, "python")
	if pythonBin == "" {
		pythonBin = "python3"
	}

	// Preview.
	fmt.Println()
	for _, ep := range entrypoints {
		svcName := serviceNameFromEntrypoint(proj.Path, ep)
		fmt.Printf("  %s %s %s  (service: %s)\n", otelInstrument, pythonBin, ep, svcName)
	}
	fmt.Print("  Run now? [Y/n]: ")
	{
		reader := bufio.NewReader(os.Stdin)
		answer, _ := reader.ReadString('\n')
		answer = strings.TrimSpace(strings.ToLower(answer))
		if answer != "" && answer != "y" && answer != "yes" {
			return
		}
	}

	// Launch each entrypoint with a per-service OTEL_SERVICE_NAME.
	fmt.Println()
	for _, ep := range entrypoints {
		svcName := serviceNameFromEntrypoint(proj.Path, ep)
		epEnvVars := make(map[string]string, len(envVars))
		for k, v := range envVars {
			epEnvVars[k] = v
		}
		epEnvVars["OTEL_SERVICE_NAME"] = svcName

		cmd := exec.Command(otelInstrument, pythonBin, ep)
		cmd.Dir = proj.Path
		cmd.Env = append(os.Environ(), envVarsToSlice(epEnvVars)...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			fmt.Printf("    Failed to start %s: %v\n", ep, err)
			continue
		}
		fmt.Printf("  Started %s as \"%s\" (PID %d)\n", ep, svcName, cmd.Process.Pid)
	}
}

// commonEntrypoints are filenames commonly used as Python project entrypoints,
// checked in priority order.
var commonEntrypoints = []string{
	"main.py",
	"app.py",
	"run.py",
	"server.py",
	"manage.py",
	"wsgi.py",
	"asgi.py",
}

// serviceNameFromEntrypoint derives a human-readable OTEL_SERVICE_NAME from an
// entrypoint path relative to the project root.
//
// Examples:
//
//	"app.py"                in project "orderschnitzel" → "orderschnitzel"
//	"s-frontend/app.py"     in project "orderschnitzel" → "orderschnitzel-s-frontend"
//	"services/api/main.py"  in project "myapp"          → "myapp-api"
func serviceNameFromEntrypoint(projectPath, entrypoint string) string {
	projectName := filepath.Base(projectPath)

	dir := filepath.Dir(entrypoint)
	if dir == "." || dir == "" {
		// Entrypoint is in the project root — use project name directly.
		return projectName
	}

	// Use the immediate parent folder of the entrypoint as the service qualifier.
	// e.g. "s-frontend/app.py" → "s-frontend", "services/api/main.py" → "api"
	servicePart := filepath.Base(dir)
	return projectName + "-" + servicePart
}

// detectPythonEntrypoints finds Python entrypoint files in a project.
// Checks pyproject.toml scripts, common filenames in the project root, and
// common filenames in immediate subdirectories (for multi-service projects).
// Returns paths relative to the project root.
func detectPythonEntrypoints(projectPath string) []string {
	var entrypoints []string

	// Try pyproject.toml [project.scripts] or [tool.poetry.scripts].
	pyproject := filepath.Join(projectPath, "pyproject.toml")
	if data, err := os.ReadFile(pyproject); err == nil {
		if ep := parseEntrypointFromPyproject(string(data)); ep != "" {
			entrypoints = append(entrypoints, ep)
		}
	}
	if len(entrypoints) > 0 {
		return entrypoints
	}

	// Check common entrypoint filenames in the project root.
	for _, name := range commonEntrypoints {
		if _, err := os.Stat(filepath.Join(projectPath, name)); err == nil {
			entrypoints = append(entrypoints, name)
		}
	}
	if len(entrypoints) > 0 {
		return entrypoints
	}

	// Check immediate subdirectories (e.g. s-frontend/app.py, s-order/app.py).
	entries, err := os.ReadDir(projectPath)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || e.Name() == "__pycache__" ||
			e.Name() == "node_modules" {
			continue
		}
		subDir := filepath.Join(projectPath, e.Name())
		for _, name := range commonEntrypoints {
			if _, err := os.Stat(filepath.Join(subDir, name)); err == nil {
				entrypoints = append(entrypoints, filepath.Join(e.Name(), name))
			}
		}
	}
	return entrypoints
}

// parseEntrypointFromPyproject extracts a script entrypoint from pyproject.toml content.
// Looks for patterns like `module:func` under [project.scripts] and converts to module path.
func parseEntrypointFromPyproject(content string) string {
	// Simple line-based scan for `name = "module:func"` or `name = "module.submod:func"`.
	inScripts := false
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			inScripts = trimmed == "[project.scripts]" || trimmed == "[tool.poetry.scripts]"
			continue
		}
		if !inScripts {
			continue
		}
		// Parse `name = "module:func"`.
		parts := strings.SplitN(trimmed, "=", 2)
		if len(parts) != 2 {
			continue
		}
		val := strings.Trim(strings.TrimSpace(parts[1]), "\"'")
		if colonIdx := strings.Index(val, ":"); colonIdx > 0 {
			// Convert module path to file: "myapp.main:run" → "myapp/main.py"
			modPath := val[:colonIdx]
			return strings.ReplaceAll(modPath, ".", "/") + ".py"
		}
	}
	return ""
}

// resolveVenvBinary finds a binary in the project's virtualenv bin directory.
// Returns the absolute path if found, otherwise returns the name for PATH lookup.
func resolveVenvBinary(projectPath, name string) string {
	for _, venvName := range []string{".venv", "venv", "env", ".env"} {
		binPath := filepath.Join(projectPath, venvName, "bin", name)
		if _, err := os.Stat(binPath); err == nil {
			return binPath
		}
		// Windows.
		binPath = filepath.Join(projectPath, venvName, "Scripts", name+".exe")
		if _, err := os.Stat(binPath); err == nil {
			return binPath
		}
	}
	return name
}

// detectProjectPip looks for a pip executable inside common virtualenv
// directories of a project.
func detectProjectPip(projectPath string) *pipCommand {
	for _, venvName := range []string{".venv", "venv", "env", ".env"} {
		pipPath := filepath.Join(projectPath, venvName, "bin", "pip")
		if _, err := os.Stat(pipPath); err == nil {
			return &pipCommand{name: pipPath}
		}
		// Windows layout.
		pipPath = filepath.Join(projectPath, venvName, "Scripts", "pip.exe")
		if _, err := os.Stat(pipPath); err == nil {
			return &pipCommand{name: pipPath}
		}
	}
	return nil
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

	// 6. Detect running Python processes and projects, offer instrumentation.
	procs := detectPythonProcesses()
	projects := detectPythonProjects()
	promptPythonInstrumentation(procs, projects, envVars)

	return nil
}
