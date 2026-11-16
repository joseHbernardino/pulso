package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

func newStartupCmd() *cobra.Command {
	var (
		platform  string
		uninstall bool
		system    bool
		printOnly bool
		webListen string
	)

	cmd := &cobra.Command{
		Use:   "startup",
		Short: "Install or uninstall Pulso as a system service",
		Long: `Install Pulso as a system service so it starts automatically on boot.
Supports systemd (Linux) and launchd (macOS).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if uninstall {
				if platform == "systemd" {
					return uninstallSystemdScope(system)
				}
				return uninstallService(platform)
			}
			if platform == "systemd" {
				exe, err := os.Executable()
				if err != nil {
					return err
				}
				cwd, err := os.Getwd()
				if err != nil {
					return err
				}
				var configPath string
				if cfgFile != "" {
					configPath, err = filepath.Abs(cfgFile)
					if err != nil {
						return err
					}
				}
				unit, err := renderSystemd(exe, cwd, dataDir(), configPath, os.Getenv("PATH"), webListen, system)
				if err != nil {
					return err
				}
				if printOnly {
					_, err = fmt.Fprint(cmd.OutOrStdout(), unit)
					return err
				}
				dir, err := systemdUnitDir(system)
				if err != nil {
					return err
				}
				if err = os.MkdirAll(dir, 0o755); err != nil {
					return err
				}
				target := filepath.Join(dir, "pulso.service")
				if err = os.WriteFile(target, []byte(unit), 0o644); err != nil {
					return err
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Serviço instalado em %s\n", target)
				control := "systemctl"
				if !system {
					control += " --user"
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Execute:\n  %s daemon-reload\n  %s enable --now pulso.service\n", control, control)
				if !system {
					_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Para iniciar sem login, um administrador deve executar: loginctl enable-linger <seu-usuario>")
				}
				return nil
			}
			return installService(platform)
		},
	}

	cmd.Flags().BoolVar(&system, "system", os.Geteuid() == 0, "Install a system-wide unit (requires root); use --system=false for a user unit")
	cmd.Flags().BoolVar(&printOnly, "print", false, "Print the unit without installing it")
	cmd.Flags().StringVar(&webListen, "web-listen", "127.0.0.1:9615", "Dashboard address after boot")
	cmd.Flags().StringVar(&platform, "platform", detectInitSystem(), "init system: systemd, launchd")
	cmd.Flags().BoolVar(&uninstall, "uninstall", false, "uninstall the startup service")

	return cmd
}

// detectInitSystem returns the appropriate init system for the current OS.
func detectInitSystem() string {
	if _, err := os.Stat("/run/systemd/system"); err == nil {
		return "systemd"
	}
	if _, err := os.Stat("/Library/LaunchDaemons"); err == nil {
		return "launchd"
	}
	return "systemd"
}

func installService(platform string) error {
	switch platform {
	case "systemd":
		return installSystemd()
	case "launchd":
		return installLaunchd()
	default:
		return fmt.Errorf("unsupported platform: %s (supported: systemd, launchd)", platform)
	}
}

func uninstallService(platform string) error {
	switch platform {
	case "systemd":
		return uninstallSystemd()
	case "launchd":
		return uninstallLaunchd()
	default:
		return fmt.Errorf("unsupported platform: %s", platform)
	}
}

// systemd arguments must retain spaces and literal percent signs without shell expansion.
func systemdQuote(value string) (string, error) {
	if strings.ContainsAny(value, "\r\n\x00") {
		return "", fmt.Errorf("service paths cannot contain line breaks")
	}
	value = strings.ReplaceAll(value, "%", "%%")
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return `"` + value + `"`, nil
}
func renderSystemd(exe, cwd, dir, configPath, path, listen string, system bool) (string, error) {
	args := []string{exe, "--data-dir", dir, "daemon", "run", "--web", "--web-listen", listen}
	if configPath != "" {
		args = append(args, "--config", configPath)
	}
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		q, err := systemdQuote(arg)
		if err != nil {
			return "", err
		}
		quoted = append(quoted, strings.ReplaceAll(q, "$", "$$"))
	}
	_, err := systemdQuote(cwd)
	if err != nil {
		return "", err
	}
	wd := strings.ReplaceAll(cwd, "%", "%%")
	env, err := systemdQuote("PATH=" + path)
	if err != nil {
		return "", err
	}
	target := "default.target"
	if system {
		target = "multi-user.target"
	}
	return fmt.Sprintf(`[Unit]
Description=Pulso Application Supervisor
Wants=network-online.target
After=network-online.target
StartLimitIntervalSec=0

[Service]
Type=simple
WorkingDirectory=%s
ExecStart=%s
Environment=%s
Restart=always
RestartSec=5
TimeoutStopSec=120
KillMode=control-group
UMask=0077
LimitNOFILE=65536

[Install]
WantedBy=%s
`, wd, strings.Join(quoted, " "), env, target), nil
}
func systemdUnitDir(system bool) (string, error) {
	if system {
		if os.Geteuid() != 0 {
			return "", fmt.Errorf("system installation requires root")
		}
		return "/etc/systemd/system", nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config/systemd/user"), nil
}
func installSystemd() error { return fmt.Errorf("use pulso startup --platform systemd") }
func uninstallSystemdScope(system bool) error {
	dir, err := systemdUnitDir(system)
	if err != nil {
		return err
	}
	control := "systemctl"
	if !system {
		control += " --user"
	}
	_, _ = fmt.Fprintf(os.Stdout, "Execute:\n  %s disable --now pulso.service\n  rm %s\n  %s daemon-reload\n", control, filepath.Join(dir, "pulso.service"), control)
	return nil
}

func uninstallSystemd() error {
	unitName := "pulso.service"
	configDir := filepath.Join(os.Getenv("HOME"), ".config", "systemd", "user")
	unitPath := filepath.Join(configDir, unitName)

	if _, err := os.Stat(unitPath); os.IsNotExist(err) {
		_, _ = fmt.Fprintln(os.Stdout, "Service not installed.")
		return nil
	}

	_, _ = fmt.Fprintln(os.Stdout, "Run the following to disable and remove:")
	_, _ = fmt.Fprintln(os.Stdout, "  systemctl --user disable --now pulso.service")
	_, _ = fmt.Fprintf(os.Stdout, "  rm %s\n", unitPath)
	_, _ = fmt.Fprintln(os.Stdout, "  systemctl --user daemon-reload")
	return nil
}

func installLaunchd() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("finding executable: %w", err)
	}

	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>io.pulso.daemon</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
        <string>daemon</string>
        <string>run</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>/tmp/pulso.launchd.log</string>
    <key>StandardErrorPath</key>
    <string>/tmp/pulso.launchd.log</string>
    <key>EnvironmentVariables</key>
    <dict>
        <key>PULSO_DAEMON</key>
        <string>1</string>
    </dict>
</dict>
</plist>
`, exe)

	label := "io.pulso.daemon.plist"
	launchDir := filepath.Join(os.Getenv("HOME"), "Library", "LaunchAgents")
	plistPath := filepath.Join(launchDir, label)

	if err := os.MkdirAll(launchDir, 0o755); err != nil {
		return fmt.Errorf("creating LaunchAgents dir: %w", err)
	}

	if err := os.WriteFile(plistPath, []byte(plist), 0o644); err != nil {
		return fmt.Errorf("writing plist: %w", err)
	}

	_, _ = fmt.Fprintf(os.Stdout, "Installed launchd agent to %s\n", plistPath)
	_, _ = fmt.Fprintln(os.Stdout, "Run the following to load:")
	_, _ = fmt.Fprintf(os.Stdout, "  launchctl load %s\n", plistPath)
	return nil
}

func uninstallLaunchd() error {
	label := "io.pulso.daemon.plist"
	launchDir := filepath.Join(os.Getenv("HOME"), "Library", "LaunchAgents")
	plistPath := filepath.Join(launchDir, label)

	if _, err := os.Stat(plistPath); os.IsNotExist(err) {
		_, _ = fmt.Fprintln(os.Stdout, "Service not installed.")
		return nil
	}

	_, _ = fmt.Fprintln(os.Stdout, "Run the following to unload and remove:")
	_, _ = fmt.Fprintf(os.Stdout, "  launchctl unload %s\n", plistPath)
	_, _ = fmt.Fprintf(os.Stdout, "  rm %s\n", plistPath)
	return nil
}
