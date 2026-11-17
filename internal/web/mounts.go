package web

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// Mount roots are kernel-provided hints, not a way to access unmounted host files.
// Explicit configuration takes precedence on filesystems with different host roots.
func (p pathResolver) withDetectedMounts() pathResolver {
	if !p.container || p.configError != nil || runtime.GOOS != "linux" {
		return p
	}
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return p
	}
	defer func() { _ = f.Close() }()
	return p.mergeMounts(parseProjectMounts(io.LimitReader(f, 2<<20)))
}

func parseProjectMounts(reader io.Reader) []pathMapping {
	unescape := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	mounts := []pathMapping{}
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 10 {
			continue
		}
		separator := -1
		for i := 6; i < len(fields); i++ {
			if fields[i] == "-" {
				separator = i
				break
			}
		}
		if separator < 6 || separator+3 >= len(fields) {
			continue
		}
		root, target := unescape.Replace(fields[3]), unescape.Replace(fields[4])
		if !filepath.IsAbs(root) || !filepath.IsAbs(target) || root != filepath.Clean(root) || root == "/" || target == "/" {
			continue
		}
		system := false
		for _, prefix := range []string{"/proc", "/sys", "/dev", "/etc", "/run", "/tmp", "/usr", "/var/lib/pulso", "/var/lib/docker"} {
			if pathWithin(target, prefix) {
				system = true
				break
			}
		}
		if system {
			continue
		}
		host := root
		// Docker named volumes do not represent a user-facing host project path.
		if strings.Contains(root, "/volumes/") && strings.HasSuffix(root, "/_data") {
			host = ""
		}
		mounts = append(mounts, pathMapping{Host: host, Container: target, Detected: true, ReadOnly: strings.Contains(","+fields[5]+",", ",ro,")})
	}
	return mounts
}

func (p pathResolver) mergeMounts(mounts []pathMapping) pathResolver {
	for _, mount := range mounts {
		if p.hostRoot != "" && pathWithin(mount.Container, p.hostRoot) {
			continue
		}
		info, err := os.Stat(mount.Container)
		if err != nil || !info.IsDir() {
			continue
		}
		found := false
		for i, configured := range p.mappings {
			if configured.Container == mount.Container {
				p.mappings[i].ReadOnly = mount.ReadOnly
				if configured.Host == "" {
					p.mappings[i].Host = mount.Host
					p.mappings[i].Detected = true
				}
				found = true
				break
			}
		}
		if !found {
			for _, configured := range p.mappings {
				if mount.Host != "" && configured.Host == mount.Host {
					mount.Host = ""
					break
				}
			}
			p.mappings = append(p.mappings, mount)
		}
	}
	sort.SliceStable(p.mappings, func(i, j int) bool { return len(p.mappings[i].Host) > len(p.mappings[j].Host) })
	return p
}
