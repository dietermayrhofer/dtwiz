package cmd

import (
	"bytes"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"

	dtclient "github.com/dynatrace-oss/dtctl/pkg/client"
	dtconfig "github.com/dynatrace-oss/dtctl/pkg/config"

	"github.com/dietermayrhofer/dtingest/pkg/dtctl"
)

// loadDtctlConfig loads the dtctl configuration, optionally switching to a
// different context via the --context flag.
func loadDtctlConfig() (*dtconfig.Config, error) {
	cfg, err := dtconfig.Load()
	if err != nil {
		return nil, fmt.Errorf(
			"%w\n\nRun 'dtctl config set-context' to configure your environment or 'dtctl auth login' to authenticate",
			err,
		)
	}

	// Apply --context override by temporarily switching the current context.
	if contextOverride != "" {
		if _, err := cfg.GetContext(contextOverride); err != nil {
			return nil, fmt.Errorf("context %q not found: %w", contextOverride, err)
		}
		cfg.CurrentContext = contextOverride
	}

	return cfg, nil
}

// newDtClient creates a dtctl Client from the loaded config.
// The client supports both OAuth PKCE tokens and plain API tokens.
func newDtClient() (*dtclient.Client, error) {
	cfg, err := loadDtctlConfig()
	if err != nil {
		return nil, err
	}
	return dtclient.NewFromConfig(cfg)
}

// environmentHint returns the Dynatrace environment URL from the --environment
// flag or the DT_ENVIRONMENT env var (flag takes precedence).
func environmentHint() string {
	if environmentFlag != "" {
		return environmentFlag
	}
	return os.Getenv("DT_ENVIRONMENT")
}

// accessToken returns the Dynatrace API access token from the --access-token
// flag or the DT_ACCESS_TOKEN env var (flag takes precedence).
// Returns an empty string when neither is set.
func accessToken() string {
	if accessTokenFlag != "" {
		return accessTokenFlag
	}
	return os.Getenv("DT_ACCESS_TOKEN")
}

// platformToken returns a Dynatrace platform token (dt0s16.*) from the
// --platform-token flag or the DT_PLATFORM_TOKEN env var (flag takes precedence).
// Returns an empty string when neither is set.
func platformToken() string {
	if platformTokenFlag != "" {
		return platformTokenFlag
	}
	return os.Getenv("DT_PLATFORM_TOKEN")
}

// getDtEnvironment returns the environment URL and token for installers that
// need raw credentials.
//
// Resolution order for the environment URL:
//  1. --environment flag / DT_ENVIRONMENT env var (if set)
//  2. Current dtctl context
//
// If no valid token exists (missing config, expired OAuth token, etc.) the
// function runs `dtctl auth login` interactively and retries once.
func getDtEnvironment() (environmentURL, token string, err error) {
	envURL, tok, err := tryGetDtEnvironment()
	if err == nil {
		return envURL, tok, nil
	}

	// Only attempt automatic recovery for auth / config problems.
	if !isAuthError(err) && !isConfigError(err) {
		return "", "", fmt.Errorf("failed to retrieve credentials: %w", err)
	}

	fmt.Println()
	if isConfigError(err) {
		fmt.Println("  No Dynatrace environment configured.")
	} else {
		fmt.Println("  Authentication required — your session has expired or is invalid.")
	}
	fmt.Println("  Running 'dtctl auth login'...")
	fmt.Println()

	loginArgs := []string{"auth", "login"}
	if hint := environmentHint(); hint != "" {
		loginArgs = append(loginArgs, "--environment="+hint)
		loginArgs = append(loginArgs, "--context="+contextNameFromURL(hint))
	} else {
		return "", "", fmt.Errorf(
			"cannot authenticate: no environment URL known\n\n" +
				"Set one with --environment or the DT_ENVIRONMENT env var:\n" +
				"  export DT_ENVIRONMENT=https://<your-env>.apps.dynatracelabs.com/",
		)
	}
	loginCmd := exec.Command(dtctl.Binary(), loginArgs...)
	loginCmd.Stdin = os.Stdin
	var loginOut bytes.Buffer
	loginCmd.Stdout = &loginOut
	loginCmd.Stderr = &loginOut
	if runErr := loginCmd.Run(); runErr != nil {
		return "", "", fmt.Errorf("dtctl auth login failed:\n%s\n%w", loginOut.String(), runErr)
	}

	fmt.Println("  Authentication successful.")
	fmt.Println()

	// Retry once after re-authentication.
	envURL, tok, err = tryGetDtEnvironment()
	if err != nil {
		return "", "", fmt.Errorf("still unable to retrieve credentials after re-authentication: %w", err)
	}
	return envURL, tok, nil
}

// tryGetDtEnvironment attempts a single credential retrieval without recovery.
// The environment URL from --environment / DT_ENVIRONMENT takes precedence over
// whatever is stored in the dtctl context.
func tryGetDtEnvironment() (environmentURL, token string, err error) {
	cfg, err := loadDtctlConfig()
	if err != nil {
		return "", "", err
	}

	ctx, err := cfg.CurrentContextObj()
	if err != nil {
		return "", "", fmt.Errorf("no current context: %w", err)
	}

	tok, err := dtclient.GetTokenWithOAuthSupport(cfg, ctx.TokenRef)
	if err != nil {
		return "", "", err
	}

	// Let the explicit flag / env var override the stored environment URL.
	envURL := ctx.Environment
	if hint := environmentHint(); hint != "" {
		envURL = hint
	}

	return envURL, tok, nil
}

// isAuthError returns true when the error is caused by an expired or revoked
// OAuth refresh token, which can be recovered by re-authenticating.
func isAuthError(err error) bool {
	return dtctl.IsAuthError(err)
}

// isConfigError returns true when no dtctl configuration exists yet, i.e. the
// user has never run `dtctl config set-context` or `dtctl auth login`.
func isConfigError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no current context") ||
		strings.Contains(msg, "config file not found") ||
		strings.Contains(msg, "no such file") ||
		strings.Contains(msg, "set-context")
}

// contextNameFromURL derives a short dtctl context name from an environment URL.
// e.g. "https://fxz0998d.dev.apps.dynatracelabs.com/" → "fxz0998d"
func contextNameFromURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return "dtingest"
	}
	// First DNS label is the environment/tenant identifier.
	host := strings.Split(u.Host, ".")[0]
	if host == "" {
		return "dtingest"
	}
	return host
}
