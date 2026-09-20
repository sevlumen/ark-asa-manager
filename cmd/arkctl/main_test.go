package main

import (
	"strings"
	"testing"
)

func TestValidateBackupName(t *testing.T) {
	valid := []string{"save-20260920.tar.gz", "backup.tar.gz"}
	for _, name := range valid {
		if err := validateBackupName(name); err != nil {
			t.Errorf("validateBackupName(%q): %v", name, err)
		}
	}
	invalid := []string{"", "../backup.tar.gz", "backup.tar", "/tmp/backup.tar.gz", "."}
	for _, name := range invalid {
		if err := validateBackupName(name); err == nil {
			t.Errorf("validateBackupName(%q) unexpectedly succeeded", name)
		}
	}
}

func TestRestoreCommandUsesConfiguredVolumes(t *testing.T) {
	t.Setenv("ARK_SAVE_VOLUME", "project_save")
	t.Setenv("ARK_BACKUPS_VOLUME", "project_backups")
	args, err := restoreCommand("backup.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	joined := ""
	for _, arg := range args {
		joined += " " + arg
	}
	if !strings.Contains(joined, "project_save:/dst") || !strings.Contains(joined, "project_backups:/src:ro") {
		t.Fatalf("restore command does not use configured volumes: %v", args)
	}
}
