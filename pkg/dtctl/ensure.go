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

	// Warn if the install directory might not be in PATH.
	if _, err := exec.LookPath("dtctl"); err != nil {
		fmt.Printf("\n  NOTE: %s may not be in your PATH.\n", dir)
		fmt.Printf("  Add it with: export PATH=\"%s:$PATH\"\n", dir)
	}

	fmt.Println()
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
