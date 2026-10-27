package convert

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"musicdl/src/backend/decode"
)

func TestConvertFormats(t *testing.T) {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg no está instalado")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "tema.wav")
	if out, err := exec.Command(ff, "-v", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=3:sample_rate=44100",
		"-ac", "2", "-metadata", "title=TituloDePrueba", "-y", src).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	r := Runner{FFmpeg: ff}
	for _, name := range []string{"mp3", "m4a", "opus", "flac", "wav"} {
		f, _ := Lookup(name)
		dst := filepath.Join(dir, "out"+f.Ext)
		if name == "wav" {
			dst = filepath.Join(dir, "out2.wav")
		}
		if err := r.Convert(context.Background(), src, dst, name, 0); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		pcm := filepath.Join(dir, name+".pcm")
		n, err := decode.ToPCM(context.Background(), ff, dst, pcm, nil)
		if err != nil {
			t.Fatalf("%s: no se puede leer lo convertido: %v", name, err)
		}
		want := int64(3 * decode.SampleRate * 2)
		if n < want-6000 || n > want+6000 { // los códecs con pérdida añaden/quitan unos ms
			t.Fatalf("%s: %d bytes de PCM, esperaba ~%d", name, n, want)
		}
		// se llaman igual que antes ⇒ no hay temporales sueltos
		if m, _ := filepath.Glob(filepath.Join(dir, ".*.converting*")); len(m) != 0 {
			t.Fatalf("%s: quedó un temporal: %v", name, m)
		}
		if name == "mp3" || name == "flac" {
			b, _ := os.ReadFile(dst)
			if !bytes.Contains(b, []byte("TituloDePrueba")) {
				t.Fatalf("%s: se perdió la etiqueta de título", name)
			}
		}
	}
	// nunca sobrescribe
	if err := r.Convert(context.Background(), src, filepath.Join(dir, "out.mp3"), "mp3", 0); err == nil {
		t.Fatal("debió negarse a sobrescribir")
	}
	// extensión incorrecta y archivo roto
	if err := r.Convert(context.Background(), src, filepath.Join(dir, "x.flac"), "mp3", 0); err == nil {
		t.Fatal("debió exigir la extensión del formato")
	}
	bad := filepath.Join(dir, "roto.wav")
	_ = os.WriteFile(bad, bytes.Repeat([]byte("no soy audio"), 200), 0o644)
	if err := r.Convert(context.Background(), bad, filepath.Join(dir, "roto.mp3"), "mp3", 0); err == nil {
		t.Fatal("debió fallar con un archivo que no es audio")
	}
	if _, err := os.Stat(filepath.Join(dir, "roto.mp3")); err == nil {
		t.Fatal("no debe quedar salida de un archivo roto")
	}
}

func TestLookupAndLossy(t *testing.T) {
	if _, ok := Lookup(".MP3"); !ok {
		t.Fatal("debe aceptar .MP3")
	}
	if _, ok := Lookup("ogg"); ok {
		t.Fatal("ogg no es formato de salida")
	}
	if !IsLossy(".mp3") || IsLossy("wav") || IsLossy(".FLAC") {
		t.Fatal("clasificación con/sin pérdida mal")
	}
}
