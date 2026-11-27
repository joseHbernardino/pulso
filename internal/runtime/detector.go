package runtime

import (
	"fmt"
	"strings"

	"github.com/rs/zerolog/log"
)

// Detector auto-detects the runtime from project files.
type Detector struct {
	runtimes []Runtime
}

// NewDetector returns a Detector with the built-in runtime adapters registered.
func NewDetector() *Detector {
	d := &Detector{
		runtimes: []Runtime{
			&GoRuntime{},
			&PythonRuntime{},
			&NodeRuntime{},
			&BunRuntime{},
			&DenoRuntime{},
			&RubyRuntime{},
			&PHPRuntime{},
			&ShellRuntime{},
		},
	}
	log.Debug().Int("count", len(d.runtimes)).Msg("detector initialized with runtimes")
	return d
}

// Detect checks each registered runtime in order and returns the first match.
func (d *Detector) Detect(dir string) (Runtime, error) {
	for _, r := range d.runtimes {
		if r.Detect(dir) {
			log.Debug().Str("runtime", r.Name()).Str("dir", dir).Msg("detected runtime")
			return r, nil
		}
	}
	return nil, fmt.Errorf("no runtime detected in %q", dir)
}

// ShellAliases are the names that select the shell runtime. They let a
// configuration say `runtime: bash` and mean the same thing as `shell`.
var ShellAliases = map[string]string{
	"shell":  "shell",
	"sh":     "shell",
	"bash":   "shell",
	"zsh":    "shell",
	"ksh":    "shell",
	"script": "shell",
}

// NormalizeName maps a runtime alias to its canonical name. Unknown names are
// returned unchanged so that Get still reports them as unknown.
func NormalizeName(name string) string {
	lowered := strings.ToLower(strings.TrimSpace(name))
	if canonical, ok := ShellAliases[lowered]; ok {
		return canonical
	}
	return lowered
}

// Get looks up a runtime by name (e.g. "go", "python", "node", "bun",
// "shell"). Shell aliases such as "bash" resolve to the shell runtime.
func (d *Detector) Get(name string) (Runtime, error) {
	name = NormalizeName(name)
	for _, r := range d.runtimes {
		if r.Name() == name {
			return r, nil
		}
	}
	return nil, fmt.Errorf("unknown runtime %q", name)
}
