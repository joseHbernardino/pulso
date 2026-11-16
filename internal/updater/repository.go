package updater

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"pulso.local/pulso/internal/version"
)

var repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*/[A-Za-z0-9][A-Za-z0-9_.-]*$`)

// releaseRepository requires an explicitly configured publishing destination.
// Development builds must never download executables from another project.
func releaseRepository() (string, error) {
	repository := strings.TrimSpace(os.Getenv("PULSO_RELEASE_REPOSITORY"))
	if repository == "" {
		repository = strings.TrimSpace(version.ReleaseRepository)
	}
	if repository == "" {
		return "", fmt.Errorf("no Pulso release repository configured; build from source or set PULSO_RELEASE_REPOSITORY to your GitHub owner/repository")
	}
	if !repositoryPattern.MatchString(repository) {
		return "", fmt.Errorf("PULSO_RELEASE_REPOSITORY must have the form owner/repository")
	}
	return repository, nil
}
