package version

var (
	Version   = "dev"
	BuildTime = "unknown"
	// ReleaseRepository is injected by the release workflow, never inferred.
	ReleaseRepository = ""
)
