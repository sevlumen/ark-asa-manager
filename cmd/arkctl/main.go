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
	case "restore":
		if len(os.Args) != 3 {
			fmt.Fprintln(os.Stderr, "usage: arkctl restore <backup.tar.gz>")
			os.Exit(2)
		}
		args = restoreCommand(os.Args[2])
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
	return []string{"run", "--rm", "-v", "ark-asa-platform_ark-save:/src:ro", "-v", "ark-asa-platform_ark-backups:/dst", "alpine:3.20", "sh", "-c", "name=/dst/ark-save-$(date -u +%Y%m%dT%H%M%SZ).tar.gz; tar -czf \"$name\" -C /src .; tar -tzf \"$name\" >/dev/null; echo \"backup=$name\""}
}

func restoreCommand(backup string) []string {
	if backup == "" || filepath.Base(backup) != backup || backup == "." || backup == ".." || !strings.HasSuffix(backup, ".tar.gz") {
		fmt.Fprintln(os.Stderr, "backup must be a file name ending in .tar.gz")
		os.Exit(2)
	}
	return []string{"run", "--rm", "-v", "ark-asa-platform_ark-save:/dst", "-v", "ark-asa-platform_ark-backups:/src:ro", "alpine:3.20", "sh", "-c", "tar -xzf \"/src/" + backup + "\" -C /dst"}
}

func exitCode(err error) int {
	if exitError, ok := err.(*exec.ExitError); ok {
		return exitError.ExitCode()
	}
	return 1
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: arkctl <start|stop|restart|status|logs|update|backup|restore>")
}
