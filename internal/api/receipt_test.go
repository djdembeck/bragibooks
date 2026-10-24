package api

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeLegacyFixture creates a legacy DB file of known content in dir.
func writeLegacyFixture(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	content := bytes.Repeat([]byte{0x41}, 256)
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatalf("write legacy fixture: %v", err)
	}
	return path
}

func TestWriteReadMigrationReceiptRoundTrip(t *testing.T) {
	dir := t.TempDir()
	legacy := writeLegacyFixture(t, dir, "db.sqlite3")

	if err := WriteMigrationReceipt(legacy, "/data/bragi.db", 2190, 6384); err != nil {
		t.Fatalf("WriteMigrationReceipt: %v", err)
	}

	receipt, exists, err := ReadMigrationReceipt(legacy)
	if err != nil {
		t.Fatalf("ReadMigrationReceipt: %v", err)
	}
	if !exists {
		t.Fatal("expected receipt to exist")
	}
	if receipt.Books != 2190 {
		t.Errorf("Books = %d, want 2190", receipt.Books)
	}
	if receipt.People != 6384 {
		t.Errorf("People = %d, want 6384", receipt.People)
	}
	if receipt.TargetNewDB != "/data/bragi.db" {
		t.Errorf("TargetNewDB = %q, want /data/bragi.db", receipt.TargetNewDB)
	}
	if receipt.LegacySize != 256 {
		t.Errorf("LegacySize = %d, want 256", receipt.LegacySize)
	}
	if len(receipt.LegacySHA256) != 64 {
		t.Errorf("LegacySHA256 = %q, want 64 hex chars", receipt.LegacySHA256)
	}
	if receipt.MigratedAt == "" {
		t.Error("MigratedAt is empty, want RFC3339 timestamp")
	}
}

func TestReadMigrationReceiptNotExist(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "db.sqlite3")

	receipt, exists, err := ReadMigrationReceipt(legacy)
	if err != nil {
		t.Fatalf("ReadMigrationReceipt: %v", err)
	}
	if exists {
		t.Error("exists = true for missing receipt file")
	}
	if receipt != nil {
		t.Errorf("receipt = %v, want nil", receipt)
	}
}

func TestLegacyMatchesReceiptFalseAfterModification(t *testing.T) {
	dir := t.TempDir()
	legacy := writeLegacyFixture(t, dir, "db.sqlite3")

	if err := WriteMigrationReceipt(legacy, "/data/bragi.db", 1, 2); err != nil {
		t.Fatalf("WriteMigrationReceipt: %v", err)
	}
	receipt, exists, err := ReadMigrationReceipt(legacy)
	if err != nil || !exists {
		t.Fatalf("ReadMigrationReceipt: exists=%v err=%v", exists, err)
	}
	if !LegacyMatchesReceipt(legacy, receipt) {
		t.Fatal("LegacyMatchesReceipt = false before modification, want true")
	}

	// Flip one byte at offset 100.
	f, err := os.OpenFile(legacy, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open legacy: %v", err)
	}
	if _, err := f.WriteAt([]byte{0x42}, 100); err != nil {
		f.Close()
		t.Fatalf("WriteAt: %v", err)
	}
	f.Close()

	if LegacyMatchesReceipt(legacy, receipt) {
		t.Error("LegacyMatchesReceipt = true after byte flip, want false")
	}
}

func TestReadMigrationReceiptCorruptContent(t *testing.T) {
	dir := t.TempDir()
	legacy := writeLegacyFixture(t, dir, "db.sqlite3")

	if err := os.WriteFile(legacy+MigrationReceiptSuffix, []byte("not-json{{{"), 0644); err != nil {
		t.Fatalf("write corrupt receipt: %v", err)
	}

	receipt, exists, err := ReadMigrationReceipt(legacy)
	if !exists {
		t.Error("exists = false, want true for corrupt receipt file")
	}
	if err == nil {
		t.Fatal("err = nil, want parse error for non-JSON receipt")
	}
	if receipt != nil {
		t.Errorf("receipt = %v, want nil", receipt)
	}
	if !strings.Contains(err.Error(), "parse receipt") {
		t.Errorf("err = %v, want parse-receipt error", err)
	}
}
