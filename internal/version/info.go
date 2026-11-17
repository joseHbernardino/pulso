package version

import (
	"runtime/debug"
	"strings"
)

// Revision is supplied for container builds, whose build context excludes .git.
var Revision = "unknown"

type Info struct {
	Version   string `json:"version"`
	Revision  string `json:"revision"`
	BuildTime string `json:"build_time"`
}

func Current() Info {
	info := Info{Version: Version, Revision: Revision, BuildTime: BuildTime}
	if info.Revision == "unknown" || info.Revision == "" {
		if build, ok := debug.ReadBuildInfo(); ok {
			for _, setting := range build.Settings {
				if setting.Key == "vcs.revision" {
					info.Revision = setting.Value
				}
			}
		}
	}
	info.Version = strings.TrimSpace(info.Version)
	if info.Version == "" {
		info.Version = "dev"
	}
	return info
}
