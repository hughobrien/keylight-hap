package bridge

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/brutella/hap"
)

func TestLoadOrGeneratePin_PersistsAndStrong(t *testing.T) {
	dir := t.TempDir()
	p1, err := loadOrGeneratePin(dir)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(p1) != 8 {
		t.Fatalf("pin length %d", len(p1))
	}
	if _, weak := hap.InvalidPins[p1]; weak {
		t.Fatalf("generated a weak pin: %s", p1)
	}
	// Second call must return the same persisted pin.
	p2, err := loadOrGeneratePin(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if p1 != p2 {
		t.Fatalf("pin not persisted: %s != %s", p1, p2)
	}
}

func TestPinFileMode(t *testing.T) {
	dir := t.TempDir()
	if _, err := loadOrGeneratePin(dir); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(dir, "pin.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("pin file mode %o", fi.Mode().Perm())
	}
}

func TestFormatPin(t *testing.T) {
	if got := FormatPin("12345678"); got != "1234-5678" {
		t.Fatalf("got %q", got)
	}
}
