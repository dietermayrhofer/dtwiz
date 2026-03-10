// Package dtctl ensures the dtctl binary is available on the system.
// If dtctl is not found in PATH, the user is prompted to download the latest
// release binary from GitHub automatically.
package dtctl

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	dtctlRepo           = "dynatrace-oss/dtctl"
	githubLatestRelease = "https://github.com/" + dtctlRepo + "/releases/latest"
)

// dtctlPath holds the absolute path to the dtctl binary when it was
// auto-installed by EnsureInstalled. Empty means dtctl was already in PATH.
var dtctlPath string

// Binary returns the path to use when invoking dtctl via exec.Command.
// If dtctl was auto-downloaded, this returns the absolute path (which avoids
// Go 1.19+'s refusal to run executables found relative to the current directory).
// Otherwise it returns "dtctl" so that normal PATH lookup is used.
func Binary() string {
	if dtctlPath != "" {
		return dtctlPath
	}
	return "dtctl"
}

type ghRelease struct {
	TagName string
	Assets  []ghAsset
}

type ghAsset struct {
	Name               string
	BrowserDownloadURL string
}

// EnsureInstalled checks if dtctl is present in PATH. If not, it prompts the
// user and downloads the latest release binary from GitHub.
func EnsureInstalled() error {
	if _, err := exec.LookPath("dtctl"); err == nil {
		return nil // already available
	}

	fmt.Println()
	fmt.Println("  dtctl is required but was not found in PATH.")
	fmt.Println("  dtctl manages Dynatrace authentication used by dtingest.")
	fmt.Println()
	fmt.Print("  Download the latest dtctl binary from GitHub? [Y/n] ")

	var answer string
	fmt.Scanln(&answer)
	answer = strings.TrimSpace(strings.ToLower(answer))
	if answer != "" && answer != "y" && answer != "yes" {
		return fmt.Errorf(
			"dtctl is required — install it manually: https://github.com/dynatrace-oss/dtctl/releases/latest",
		)
	}

	fmt.Println()
	fmt.Println("  Fetching latest dtctl release info...")
	release, err := fetchLatestRelease()
	if err != nil {
		return fmt.Errorf("failed to fetch release info: %w", err)
	}

	assetURL, assetName, err := findAsset(release)
	if err != nil {
		return err
	}

	dir, err := installDir()
	if err != nil {
		return err
	}

	destPath := filepath.Join(dir, "dtctl")
	if runtime.GOOS == "windows" {
		destPath += ".exe"
	}

	fmt.Printf("  Downloading %s (%s)...\n", assetName, release.TagName)
	if err := downloadFile(assetURL, destPath); err != nil {
		return fmt.Errorf("download failed: %w", err)
	}

	if err := os.Chmod(destPath, 0755); err != nil {
		return fmt.Errorf("chmod failed: %w", err)
	}

	fmt.Printf("  Installed dtctl to %s\n", destPath)

	// Store the absolute path so Binary() returns it for all subsequent exec calls.
	dtctlPath = destPath

	// If the install directory is not in PATH, add it to the shell profile automatically.
	if _, err := exec.LookPath("dtctl"); err != nil {
		if addErr := addToPath(dir); addErr != nil {
			fmt.Printf("\n  NOTE: %s may not be in your PATH.\n", dir)
			fmt.Printf("  Add it with: export PATH=\"%s:$PATH\"\n", dir)
		}
	}

	fmt.Println()
	return nil
}

// addToPath appends an export PATH line to the user's shell profile and updates
// the current process's PATH so dtctl is immediately usable.
func addToPath(dir string) error {
	if runtime.GOOS == "windows" {
		// On Windows, use the registry-backed user PATH.
		return addToPathWindows(dir)
	}

	profileFile := shellProfileFile()
	if profileFile == "" {
		return fmt.Errorf("could not determine shell profile")
	}

	exportLine := fmt.Sprintf("export PATH=\"%s:$PATH\"", dir)

	// Check if the directory is already referenced in the profile.
	if data, err := os.ReadFile(profileFile); err == nil {
		if strings.Contains(string(data), dir) {
			// Already present — just update the running process's PATH.
			os.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			return nil
		}
	}

	f, err := os.OpenFile(profileFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = fmt.Fprintf(f, "\n# Added by dtingest installer\n%s\n", exportLine)
	if err != nil {
		return err
	}

	// Update the running process's PATH so LookPath succeeds immediately.
	os.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	fmt.Printf("\n  Added %s to PATH in %s\n", dir, profileFile)
	return nil
}

// shellProfileFile returns the path to the current user's shell profile.
func shellProfileFile() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	shell := os.Getenv("SHELL")
	switch {
	case strings.HasSuffix(shell, "/zsh"):
		return filepath.Join(home, ".zshrc")
	case strings.HasSuffix(shell, "/bash"):
		if runtime.GOOS == "darwin" {
			return filepath.Join(home, ".bash_profile")
		}
		return filepath.Join(home, ".bashrc")
	default:
		return filepath.Join(home, ".profile")
	}
}

// addToPathWindows adds the directory to the user-level PATH environment variable
// on Windows via os.Setenv (process) and advises the user about persistence.
func addToPathWindows(dir string) error {
	// Update the running process so LookPath works immediately.
	os.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	fmt.Printf("\n  Added %s to PATH for this session.\n", dir)
	fmt.Printf("  To make it permanent, add it via System → Environment Variables.\n")
	return nil
}

// fetchLatestRelease resolves the latest dtctl release by following the GitHub
// /releases/latest redirect (no API call, avoids rate limits). It then builds
// asset download URLs from the known naming convention.
func fetchLatestRelease() (*ghRelease, error) {
	// Use a client that does NOT follow redirects so we can read the Location header.
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	req, err := http.NewRequest("GET", githubLatestRelease, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "dtingest")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	resp.Body.Close()

	loc := resp.Header.Get("Location")
	if loc == "" {
		return nil, fmt.Errorf("no redirect from %s (HTTP %d)", githubLatestRelease, resp.StatusCode)
	}

	// Location looks like https://github.com/dynatrace-oss/dtctl/releases/tag/v0.x.y
	parts := strings.Split(loc, "/")
	tag := parts[len(parts)-1]
	if tag == "" {
		return nil, fmt.Errorf("could not extract tag from redirect URL: %s", loc)
	}
	version := strings.TrimPrefix(tag, "v")

	// Build known asset names for all platforms.
	// dtctl naming: dtctl_{version}_{os}_{arch}.tar.gz / .zip
	type platform struct {
		os, arch string
	}
	platforms := []platform{
		{"darwin", "arm64"}, {"darwin", "amd64"},
		{"linux", "arm64"}, {"linux", "amd64"},
		{"windows", "arm64"}, {"windows", "amd64"},
	}

	var assets []ghAsset
	for _, p := range platforms {
		ext := "tar.gz"
		if p.os == "windows" {
			ext = "zip"
		}
		name := fmt.Sprintf("dtctl_%s_%s_%s.%s", version, p.os, p.arch, ext)
		url := fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", dtctlRepo, tag, name)
		assets = append(assets, ghAsset{Name: name, BrowserDownloadURL: url})
	}

	return &ghRelease{TagName: tag, Assets: assets}, nil
}

// findAsset locates the release asset matching the current OS and architecture.
func findAsset(rel *ghRelease) (url, name string, err error) {
	goos := runtime.GOOS
	goarch := runtime.GOARCH

	// Include common alternative names used in release asset filenames.
	archAliases := []string{goarch}
	switch goarch {
	case "amd64":
		archAliases = append(archAliases, "x86_64")
	case "arm64":
		archAliases = append(archAliases, "aarch64")
	}

	for _, asset := range rel.Assets {
		lower := strings.ToLower(asset.Name)
		if !strings.Contains(lower, goos) {
			continue
		}
		for _, arch := range archAliases {
			if strings.Contains(lower, arch) {
				return asset.BrowserDownloadURL, asset.Name, nil
			}
		}
	}

	return "", "", fmt.Errorf(
		"no dtctl binary found for %s/%s in release %s\nDownload manually: https://github.com/dynatrace-oss/dtctl/releases/latest",
		goos, goarch, rel.TagName,
	)
}

// installDir returns the first writable directory suitable for installing dtctl.
// It prefers /usr/local/bin, then ~/.local/bin, then ~/bin.
func installDir() (string, error) {
	candidates := []string{"/usr/local/bin"}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates,
			filepath.Join(home, ".local", "bin"),
			filepath.Join(home, "bin"),
		)
	}

	for _, dir := range candidates {
		if err := os.MkdirAll(dir, 0755); err != nil {
			continue
		}
		// Probe write access without leaving files behind.
		probe := filepath.Join(dir, ".dtingest_probe")
		f, err := os.Create(probe)
		if err != nil {
			continue
		}
		f.Close()
		os.Remove(probe)
		return dir, nil
	}

	return "", fmt.Errorf(
		"no writable installation directory found\n" +
			"Try running with sudo, or install dtctl manually: https://github.com/dynatrace-oss/dtctl/releases/latest",
	)
}

// downloadFile downloads url to dest, writing atomically via a temp file.
func downloadFile(url, dest string) error {
	resp, err := http.Get(url) //nolint:noctx
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %s", resp.Status)
	}

	tmp := dest + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}

	_, copyErr := io.Copy(f, resp.Body)
	closeErr := f.Close()
	if copyErr != nil {
		os.Remove(tmp)
		return copyErr
	}
	if closeErr != nil {
		os.Remove(tmp)
		return closeErr
	}

	return os.Rename(tmp, dest)
}
