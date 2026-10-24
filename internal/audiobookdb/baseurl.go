package audiobookdb

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

// TrustedBaseURL canonicalizes and validates an audiobookdb base URL
// against the trusted-origin policy (finding: SSRF). Only https URLs whose
// host is audiobookdb.org or a subdomain are accepted. localhost/127.* and
// http: URLs are accepted only in dev, behind BRAGIBOOKS_ALLOW_LOCAL_ADB
// set to 1/true/yes/on. It is the single source of the policy, shared by
// the settings validator (update time) and the client (request time).
func TrustedBaseURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("audiobookdb base URL: %v", err)
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", fmt.Errorf("audiobookdb base URL: must be an absolute HTTP(S) URL")
	}

	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if host == "" {
		return "", fmt.Errorf("audiobookdb base URL: missing host")
	}
	if parsed.Scheme == "https" &&
		(host == "audiobookdb.org" || strings.HasSuffix(host, ".audiobookdb.org")) {
		return parsed.String(), nil
	}
	dev := false
	switch strings.ToLower(strings.TrimSpace(os.Getenv("BRAGIBOOKS_ALLOW_LOCAL_ADB"))) {
	case "1", "true", "yes", "on":
		dev = true
	}
	if dev && parsed.Scheme == "http" &&
		(host == "localhost" || strings.HasPrefix(host, "127.") || host == "::1") {
		return parsed.String(), nil
	}
	return "", fmt.Errorf("audiobookdb base URL host %q is not a trusted origin (only https://audiobookdb.org and its subdomains are allowed)", host)
}
