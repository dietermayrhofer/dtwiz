package dtctl

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// AppsContextInfo holds the name and environment URL of a dtctl context
// that uses the Grail platform (.apps.) URL.
type AppsContextInfo struct {
	Name        string
	Environment string
}

// AppsContext inspects `dtctl config get-contexts` and returns the info
// for the first context whose environment URL contains ".apps." (i.e. the Grail
// platform URL). DQL log queries require such a context — classic env URLs
// return 403. Returns nil if no apps-URL context is found or if the active
// context is already an apps URL.
func AppsContext() *AppsContextInfo {
	out, err := exec.Command(Binary(), "config", "get-contexts", "-o", "json", "--plain").Output()
	if err != nil {
		return nil // non-fatal: fall back to default context
	}

	var contexts []struct {
		Name        string `json:"Name"`
		Environment string `json:"Environment"`
		Current     string `json:"Current"`
	}
	if err := json.Unmarshal(out, &contexts); err != nil {
		return nil
	}

	// If the active context already uses an apps URL, no override needed.
	for _, c := range contexts {
		if c.Current == "*" && strings.Contains(c.Environment, ".apps.") {
			return nil
		}
	}
	// Otherwise pick the first apps-URL context available.
	for _, c := range contexts {
		if strings.Contains(c.Environment, ".apps.") {
			return &AppsContextInfo{Name: c.Name, Environment: c.Environment}
		}
	}
	return nil
}

// IsTokenRefreshError returns true when the dtctl stderr output indicates an
// expired or revoked OAuth refresh token.
func IsTokenRefreshError(stderr string) bool {
	return strings.Contains(stderr, "failed to refresh token") ||
		strings.Contains(stderr, "invalid_grant") ||
		strings.Contains(stderr, "UNSUCCESSFUL_OAUTH_REFRESH_TOKEN_VALIDATION_FAILED")
}

// IsAuthError returns true when err indicates an expired, revoked, or missing
// authentication credential that can be recovered by re-authenticating.
func IsAuthError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "invalid_grant") ||
		strings.Contains(msg, "refresh token") ||
		strings.Contains(msg, "token refresh failed") ||
		strings.Contains(msg, "unauthorized") ||
		strings.Contains(msg, "401")
}

// Reauth runs `dtctl auth login` interactively for the given context,
// opening the browser so the user can complete the OAuth flow.
func Reauth(ctx *AppsContextInfo) error {
	fmt.Printf("\n\n  Session expired for dtctl context %q. Re-authenticating...\n", ctx.Name)
	cmd := exec.Command(Binary(), "auth", "login", "--context", ctx.Name, "--environment", ctx.Environment)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("dtctl auth login failed: %w", err)
	}
	return nil
}
