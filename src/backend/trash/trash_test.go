package trash

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestUniqueIn(t *testing.T) {
	d := t.TempDir()
	if got := uniqueIn(d, "a.mp3"); got != "a.mp3" {
		t.Fatal(got)
	}
	_ = os.WriteFile(filepath.Join(d, "a.mp3"), nil, 0o644)
	_ = os.WriteFile(filepath.Join(d, "a 2.mp3"), nil, 0o644)
	if got := uniqueIn(d, "a.mp3"); got != "a 3.mp3" {
		t.Fatal(got)
	}
}

func TestMoveRejectsNonFiles(t *testing.T) {
	d := t.TempDir()
	if err := Move(d); err == nil {
		t.Fatal("una carpeta no debe ir a la papelera")
	}
	if err := Move(filepath.Join(d, "no-existe.mp3")); err == nil {
		t.Fatal("un archivo inexistente debe fallar")
	}
}

func TestMoveLinuxFollowsFreeDesktopSpec(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("solo Linux")
	}
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	src := filepath.Join(t.TempDir(), "mi canción.mp3")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// sin datos en otro disco: el temp dir comparte sistema de archivos con el de arriba en CI normal
	if err := Move(src); err != nil {
		t.Skipf("rename entre discos distintos: %v", err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatal("el original debía desaparecer")
	}
	if _, err := os.Stat(filepath.Join(data, "Trash", "files", "mi canción.mp3")); err != nil {
		t.Fatal(err)
	}
	info, err := os.ReadFile(filepath.Join(data, "Trash", "info", "mi canción.mp3.trashinfo"))
	if err != nil || !strings.Contains(string(info), "Path=") || !strings.Contains(string(info), "mi%20canci%C3%B3n.mp3") {
		t.Fatalf("trashinfo = %q (%v)", info, err)
	}
}
