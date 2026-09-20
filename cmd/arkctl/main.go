package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	var args []string
	switch os.Args[1] {
	case "start":
		args = []string{"compose", "--profile", "ark", "up", "-d", "ark"}
	case "stop":
		args = []string{"compose", "stop", "ark"}
	case "restart":
		args = []string{"compose", "restart", "ark"}
	case "status":
		args = []string{"compose", "ps", "--all"}
	case "logs":
		args = []string{"compose", "logs", "--tail=200", "ark"}
	case "update":
		args = []string{"compose", "--profile", "ark", "up", "-d", "--build", "ark"}
	case "backup":
		args = backupCommand()
	case "verify":
		if len(os.Args) != 3 {
			fmt.Fprintln(os.Stderr, "usage: arkctl verify <backup.tar.gz>")
			os.Exit(2)
		}
		var err error
		args, err = verifyCommand(os.Args[2])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
	case "restore":
		if len(os.Args) != 3 {
			fmt.Fprintln(os.Stderr, "usage: arkctl restore <backup.tar.gz>")
			os.Exit(2)
		}
		var err error
		args, err = restoreCommand(os.Args[2])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		if err := ensureInstanceStopped(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
	default:
		usage()
		os.Exit(2)
	}

	command := exec.Command("docker", args...)
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		os.Exit(exitCode(err))
	}
}

func backupCommand() []string {
	saveVolume := getenv("ARK_SAVE_VOLUME", "ark-asa-platform_ark-save")
	backupVolume := getenv("ARK_BACKUPS_VOLUME", "ark-asa-platform_ark-backups")
	script := `set -eu
name=/dst/ark-save-$(date -u +%Y%m%dT%H%M%SZ).tar.gz
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
tar -czf "$tmp/payload.tar.gz" -C /src .
tar -tzf "$tmp/payload.tar.gz" >/dev/null
(cd /src && find . -type f -print | sort | while IFS= read -r file; do sha256sum "$file"; done) > "$tmp/manifest"
printf 'ARKCTL_BACKUP_V1\n' >> "$tmp/manifest"
tar -czf "$name" -C "$tmp" payload.tar.gz manifest
tar -tzf "$name" >/dev/null
echo "backup=$name"`
	return []string{"run", "--rm", "-v", saveVolume + ":/src:ro", "-v", backupVolume + ":/dst", "alpine:3.20", "sh", "-c", script}
}

func restoreCommand(backup string) ([]string, error) {
	if err := validateBackupName(backup); err != nil {
		return nil, err
	}
	saveVolume := getenv("ARK_SAVE_VOLUME", "ark-asa-platform_ark-save")
	backupVolume := getenv("ARK_BACKUPS_VOLUME", "ark-asa-platform_ark-backups")
	script := `set -eu
archive="/src/` + backup + `"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
tar -xzf "$archive" -C "$tmp"
if [ -f "$tmp/payload.tar.gz" ] && [ -f "$tmp/manifest" ]; then
  mkdir "$tmp/data"
  tar -xzf "$tmp/payload.tar.gz" -C "$tmp/data"
  sed '/^ARKCTL_BACKUP_V1$/d' "$tmp/manifest" > "$tmp/checksums"
  if [ -s "$tmp/checksums" ]; then
    (cd "$tmp/data" && sha256sum -c "$tmp/checksums")
  fi
  tar -xzf "$tmp/payload.tar.gz" -C /dst
else
  tar -xzf "$archive" -C /dst
fi`
	return []string{"run", "--rm", "-v", saveVolume + ":/dst", "-v", backupVolume + ":/src:ro", "alpine:3.20", "sh", "-c", script}, nil
}

func verifyCommand(backup string) ([]string, error) {
	if err := validateBackupName(backup); err != nil {
		return nil, err
	}
	backupVolume := getenv("ARK_BACKUPS_VOLUME", "ark-asa-platform_ark-backups")
	script := `set -eu
archive="/src/` + backup + `"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
tar -xzf "$archive" -C "$tmp"
if [ ! -f "$tmp/payload.tar.gz" ] || [ ! -f "$tmp/manifest" ]; then
  echo 'backup has no ARKCTL manifest' >&2
  exit 1
fi
mkdir "$tmp/data"
tar -xzf "$tmp/payload.tar.gz" -C "$tmp/data"
sed '/^ARKCTL_BACKUP_V1$/d' "$tmp/manifest" > "$tmp/checksums"
if [ -s "$tmp/checksums" ]; then
  (cd "$tmp/data" && sha256sum -c "$tmp/checksums")
fi
echo "verified=$archive"`
	return []string{"run", "--rm", "-v", backupVolume + ":/src:ro", "alpine:3.20", "sh", "-c", script}, nil
}

func validateBackupName(backup string) error {
	if backup == "" || filepath.Base(backup) != backup || backup == "." || backup == ".." || !strings.HasSuffix(backup, ".tar.gz") {
		return fmt.Errorf("backup must be a file name ending in .tar.gz")
	}
	return nil
}

func ensureInstanceStopped() error {
	command := exec.Command("docker", "compose", "ps", "--status", "running", "-q", "ark")
	output, err := command.Output()
	if err != nil {
		return fmt.Errorf("could not determine ARK state: %w", err)
	}
	if strings.TrimSpace(string(output)) != "" {
		return fmt.Errorf("refusing restore while the ARK instance is running; run arkctl stop first")
	}
	return nil
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func exitCode(err error) int {
	if exitError, ok := err.(*exec.ExitError); ok {
		return exitError.ExitCode()
	}
	return 1
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: arkctl <start|stop|restart|status|logs|update|backup|verify|restore>")
}
