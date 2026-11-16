package main

import (
	"strings"
	"testing"
)

func TestRenderSystemdRecoveryUnit(t *testing.T) {
	for _, system := range []bool{false, true} {
		unit, err := renderSystemd("/opt/pulso bin/pulso", "/srv/my apps", "/var/lib/pulso", "/etc/pulso/pulso.yaml", "/usr/local/bin:/usr/bin", "127.0.0.1:9615", system)
		if err != nil {
			t.Fatal(err)
		}
		for _, value := range []string{"Type=simple", `"/opt/pulso bin/pulso"`, `"--data-dir" "/var/lib/pulso"`, `"daemon" "run"`, `"--web"`, `"--config" "/etc/pulso/pulso.yaml"`, `WorkingDirectory=/srv/my apps`, "Restart=always", "KillMode=control-group", "UMask=0077"} {
			if !strings.Contains(unit, value) {
				t.Errorf("unit missing %s", value)
			}
		}
		if strings.Contains(unit, "multi-user.target") != system {
			t.Fatal("wrong service scope")
		}
	}
}
func TestSystemdQuoteRejectsInjectedDirectives(t *testing.T) {
	if _, err := systemdQuote("/bin/pulso\nExecStart=/bin/other"); err == nil {
		t.Fatal("newline accepted")
	}
	q, err := systemdQuote(`/opt/a%b/"pulso"`)
	if err != nil || q != `"/opt/a%%b/\"pulso\""` {
		t.Fatalf("quote=%s err=%v", q, err)
	}
}

func TestSystemdExecKeepsLiteralDollarPaths(t *testing.T) {
	unit, err := renderSystemd("/opt/$APP/pulso", "/srv", "/data/$NAME", "", "/usr/bin", "127.0.0.1:9615", true)
	if err != nil || !strings.Contains(unit, "/opt/$$APP/pulso") || !strings.Contains(unit, "/data/$$NAME") {
		t.Fatalf("literal path expanded: %s %v", unit, err)
	}
}
