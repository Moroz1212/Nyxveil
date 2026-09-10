package updater

import (
	"fmt"
	"net/url"
	"regexp"
)

var releasePath = regexp.MustCompile(`^/Moroz1212/Nyxveil/releases/(download/server-v[0-9]+\.[0-9]+\.[0-9]+|latest/download)/[a-zA-Z0-9_.-]+$`)

// ValidateReleaseURL is the production entry-point boundary before downloading
// unsigned manifests or their assets. Local/test sources require explicit mode.
func ValidateReleaseURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != "github.com" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || !releasePath.MatchString(u.Path) {
		return fmt.Errorf("updater: production source must be a Moroz1212/Nyxveil GitHub Release")
	}
	return nil
}
