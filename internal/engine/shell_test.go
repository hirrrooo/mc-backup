package engine

import "testing"

func TestShellQuoteSingleQuotesUnsafePath(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"/volume1/backups", "'/volume1/backups'"},
		{"/volume 1/backups", "'/volume 1/backups'"},
		{"/volume'1/backups", "'/volume'\\''1/backups'"},
		{"", "''"},
	}

	for _, tt := range tests {
		got := shellQuote(tt.in)
		if got != tt.want {
			t.Errorf("shellQuote(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestRemoteNASCommandsQuoteConfiguredPaths(t *testing.T) {
	nas := NASConfig{
		SSHUser:  "backup",
		SSHHost:  "nas.local",
		DestRoot: "/volume 1/backups",
	}

	ready := nasReadyCommand(nas)
	if ready != "test -f '/volume 1/backups/.nas-ready'" {
		t.Fatalf("nasReadyCommand() = %q", ready)
	}

	mkdir := nasMkdirCommand("/volume 1/backups/minecraft/server one")
	if mkdir != "mkdir -p '/volume 1/backups/minecraft/server one'" {
		t.Fatalf("nasMkdirCommand() = %q", mkdir)
	}

	days := pruneNASByDaysCommand("/volume 1/backups", "minecraft", "server one", 7)
	wantDays := "find '/volume 1/backups/minecraft/server one' -mindepth 1 -maxdepth 1 -type d -name '[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]-[0-9][0-9][0-9][0-9]' -mtime +7 -exec rm -rf {} +"
	if days != wantDays {
		t.Fatalf("pruneNASByDaysCommand() = %q, want %q", days, wantDays)
	}

	count := pruneNASByCountCommand("/volume 1/backups", "minecraft", "server one", 3)
	wantCount := "ls -dt '/volume 1/backups/minecraft/server one'/[0-9]*-[0-9]* 2>/dev/null | tail -n +4 | tr '\\n' '\\0' | xargs -0 -r rm -rf --"
	if count != wantCount {
		t.Fatalf("pruneNASByCountCommand() = %q, want %q", count, wantCount)
	}
}

func TestLatestNASSnapshotCommandQuotesConfiguredPath(t *testing.T) {
	nasDir := "/volume 'one/backups/mine craft/server one"

	got := latestNASSnapshotCommand(nasDir)
	want := "ls -dt '/volume '\\''one/backups/mine craft/server one'/[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]-[0-9][0-9][0-9][0-9] 2>/dev/null | head -1"
	if got != want {
		t.Fatalf("latestNASSnapshotCommand() = %q, want %q", got, want)
	}
}
