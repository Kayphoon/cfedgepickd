package hosts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdateAndRestore(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hosts")
	backupDir := filepath.Join(dir, "backups")
	original := "127.0.0.1 localhost\n127.0.0.1 region1.v2.argotunnel.com old.local\n"
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}
	backup, err := Update(path, backupDir, []Mapping{
		{Hostname: "region1.v2.argotunnel.com", IP: "198.41.1.1"},
		{Hostname: "region2.v2.argotunnel.com", IP: "198.41.1.2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "198.41.1.1 region1.v2.argotunnel.com") {
		t.Fatalf("missing replacement:\n%s", text)
	}
	if !strings.Contains(text, "127.0.0.1\told.local") {
		t.Fatalf("unrelated host not preserved:\n%s", text)
	}
	if !strings.Contains(text, "198.41.1.2 region2.v2.argotunnel.com") {
		t.Fatalf("missing append:\n%s", text)
	}
	if err := Restore(path, backup); err != nil {
		t.Fatal(err)
	}
	restored, _ := os.ReadFile(path)
	if string(restored) != original {
		t.Fatalf("restore mismatch:\n%s", restored)
	}
}
