package updater

import (
	"context"
	"testing"

	"pulso.local/pulso/internal/version"
)

func TestReleaseRepository(t *testing.T) {
	original := version.ReleaseRepository
	t.Cleanup(func() { version.ReleaseRepository = original })
	for _, tt := range []struct {
		name, environment, embedded, want string
		wantError                         bool
	}{
		{"unconfigured", "", "", "", true},
		{"explicit repository", "team/pulso", "", "team/pulso", false},
		{"release build", "", "publisher/app", "publisher/app", false},
		{"explicit override", "new-team/pulso", "publisher/app", "new-team/pulso", false},
		{"reject URL", "https://github.com/team/pulso", "", "", true},
		{"reject traversal", "team/../another", "", "", true},
		{"reject missing owner", "/pulso", "", "", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("PULSO_RELEASE_REPOSITORY", tt.environment)
			version.ReleaseRepository = tt.embedded
			got, err := releaseRepository()
			if (err != nil) != tt.wantError || got != tt.want {
				t.Fatalf("releaseRepository() = %q, %v; want %q, error=%v", got, err, tt.want, tt.wantError)
			}
		})
	}
}

func TestUpdatesRequirePublishingDestination(t *testing.T) {
	t.Setenv("PULSO_RELEASE_REPOSITORY", "")
	original := version.ReleaseRepository
	version.ReleaseRepository = ""
	t.Cleanup(func() { version.ReleaseRepository = original })
	if _, err := CheckForUpdate(context.Background()); err == nil {
		t.Fatal("unconfigured build must refuse update checks")
	}
	if err := SelfUpdate(context.Background(), "v1.0.0"); err == nil {
		t.Fatal("unconfigured build must refuse binary replacement")
	}
}
