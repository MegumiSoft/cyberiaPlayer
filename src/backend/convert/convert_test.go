package convert

import (
	"bytes"
	"context"
	"encoding/binary"
	"math"
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
	for _, name := range []string{"mp3", "m4a", "opus", "aac", "flac", "wav"} {
		f, _ := Lookup(name)
		dst := filepath.Join(dir, "out"+f.Ext)
		if name == "wav" {
			dst = filepath.Join(dir, "out2.wav")
		}
		if name == "aac" { // el .aac crudo no lleva etiquetas
			dst = filepath.Join(dir, "out.aac")
		}
		if err := r.Convert(context.Background(), src, dst, name, Options{}); err != nil {
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
	if err := r.Convert(context.Background(), src, filepath.Join(dir, "out.mp3"), "mp3", Options{}); err == nil {
		t.Fatal("debió negarse a sobrescribir")
	}
	// extensión incorrecta y archivo roto
	if err := r.Convert(context.Background(), src, filepath.Join(dir, "x.flac"), "mp3", Options{}); err == nil {
		t.Fatal("debió exigir la extensión del formato")
	}
	bad := filepath.Join(dir, "roto.wav")
	_ = os.WriteFile(bad, bytes.Repeat([]byte("no soy audio"), 200), 0o644)
	if err := r.Convert(context.Background(), bad, filepath.Join(dir, "roto.mp3"), "mp3", Options{}); err == nil {
		t.Fatal("debió fallar con un archivo que no es audio")
	}
	if _, err := os.Stat(filepath.Join(dir, "roto.mp3")); err == nil {
		t.Fatal("no debe quedar salida de un archivo roto")
	}
}

func TestVBRAndQualityValidation(t *testing.T) {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg no está instalado")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "t.wav")
	if out, err := exec.Command(ff, "-v", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=3", "-y", src).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	r := Runner{FFmpeg: ff}
	v0, v128 := 0, 128
	if err := r.Convert(context.Background(), src, filepath.Join(dir, "vbr.mp3"), "mp3", Options{VBR: &v0}); err != nil {
		t.Fatalf("VBR V0: %v", err)
	}
	if err := r.Convert(context.Background(), src, filepath.Join(dir, "cbr.mp3"), "mp3", Options{Bitrate: &v128}); err != nil {
		t.Fatalf("128 kbps: %v", err)
	}
	// una tasa fija y VBR a la vez, bitrates fuera de lista, VBR donde no existe: error claro, sin invocar ffmpeg
	bad, v9 := 999, 9
	for _, o := range []struct {
		f string
		o Options
	}{
		{"mp3", Options{Bitrate: &v128, VBR: &v0}},
		{"mp3", Options{Bitrate: &bad}},
		{"mp3", Options{VBR: &v9}},
		{"m4a", Options{VBR: &v0}},
	} {
		if err := Validate(o.f, o.o); err == nil {
			t.Fatalf("debió rechazar %+v en %s", o.o, o.f)
		}
	}
	// en formatos sin pérdida la calidad se ignora
	if err := Validate("flac", Options{Bitrate: &bad}); err != nil {
		t.Fatalf("flac ignora kbps: %v", err)
	}
}

func TestPublishNeverOverwrites(t *testing.T) {
	dir := t.TempDir()
	tmp := filepath.Join(dir, "a.tmp")
	out := filepath.Join(dir, "a.out")
	os.WriteFile(tmp, []byte("nuevo"), 0o644)
	os.WriteFile(out, []byte("viejo"), 0o644)
	if err := publish(tmp, out); err == nil {
		t.Fatal("debió negarse a reemplazar")
	}
	if b, _ := os.ReadFile(out); string(b) != "viejo" {
		t.Fatalf("se sobrescribió: %q", b)
	}
	os.Remove(out)
	if err := publish(tmp, out); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(out); string(b) != "nuevo" {
		t.Fatalf("contenido: %q", b)
	}
	if _, err := os.Stat(tmp); err == nil {
		t.Fatal("el temporal debió desaparecer")
	}
}

func TestFormatTable(t *testing.T) {
	if len(Formats()) != 6 || len(LossyExts()) == 0 {
		t.Fatal("tabla de formatos incompleta")
	}
	for _, f := range Formats() {
		if f.Lossy && (f.Default == 0 || !contains(f.Bitrates, f.Default)) {
			t.Fatalf("%s: el valor predeterminado %d no está entre sus bitrates", f.ID, f.Default)
		}
		if !f.Lossy && (f.Default != 0 || len(f.Bitrates) != 0) {
			t.Fatalf("%s: sin pérdida no lleva bitrates", f.ID)
		}
	}
}

func TestLookupAndLossy(t *testing.T) {
	if _, ok := Lookup(".MP3"); !ok {
		t.Fatal("debe aceptar .MP3")
	}
	if _, ok := Lookup("ogg"); ok {
		t.Fatal("ogg no es formato de salida")
	}
	if !IsLossy(".mp3") || !IsLossy("OGG") || IsLossy("wav") || IsLossy(".FLAC") {
		t.Fatal("clasificación con/sin pérdida mal")
	}
}

// writeClickWAV escribe un WAV mono de 44,1 kHz / 16 bits con un chasquido de 20 ms que empieza en atSec.
func writeClickWAV(t *testing.T, path string, atSec float64) {
	t.Helper()
	const sr = 44100
	n := sr * 2
	pcm := make([]byte, n*2)
	for i := int(atSec * sr); i < int(atSec*sr)+sr/50 && i < n; i++ {
		v := int16(20000 * math.Sin(2*math.Pi*1000*float64(i)/sr))
		pcm[2*i], pcm[2*i+1] = byte(v), byte(v>>8)
	}
	var b bytes.Buffer
	w := func(v any) { _ = binary.Write(&b, binary.LittleEndian, v) }
	b.WriteString("RIFF")
	w(uint32(36 + len(pcm)))
	b.WriteString("WAVEfmt ")
	w(uint32(16))
	w(uint16(1))
	w(uint16(1))
	w(uint32(sr))
	w(uint32(sr * 2))
	w(uint16(2))
	w(uint16(16))
	b.WriteString("data")
	w(uint32(len(pcm)))
	b.Write(pcm)
	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Las rejillas y los cues se copian tal cual al archivo convertido, así que el audio convertido debe empezar en el
// mismo instante. Los codificadores con pérdida añaden muestras de relleno al inicio (LAME ≈ 25 ms, AAC ≈ 23 ms) y
// lo compensan con metadatos de «gapless» (etiqueta Xing/LAME, lista de edición, pre-skip de Opus): este test
// comprueba que ffmpeg SÍ los escribe (p. ej. no se ha desactivado -write_xing). Un reproductor que los ignore
// verá el audio ~25 ms tarde: eso no se puede arreglar desde aquí y está documentado en el README.
func TestConvertedAudioStartsAtSameInstant(t *testing.T) {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg no está instalado")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "click.wav")
	writeClickWAV(t, src, 0.5)
	firstOnset := func(file string) float64 {
		pcm := filepath.Join(dir, filepath.Base(file)+".pcm")
		n, err := decode.ToPCM(context.Background(), ff, file, pcm, nil)
		if err != nil || n == 0 {
			t.Fatalf("%s: %v", file, err)
		}
		b, _ := os.ReadFile(pcm)
		for i := 0; i+1 < len(b); i += 2 {
			v := int16(uint16(b[i]) | uint16(b[i+1])<<8)
			if v > 5000 || v < -5000 {
				return float64(i/2) / decode.SampleRate
			}
		}
		t.Fatalf("%s: no se encontró el chasquido", file)
		return 0
	}
	want := firstOnset(src)
	r := Runner{FFmpeg: ff}
	for _, name := range []string{"mp3", "m4a", "opus", "flac"} {
		f, _ := Lookup(name)
		dst := filepath.Join(dir, "click"+f.Ext)
		if err := r.Convert(context.Background(), src, dst, name, Options{}); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if d := math.Abs(firstOnset(dst)-want) * 1000; d > 3 {
			t.Errorf("%s: el audio convertido empieza %.1f ms corrido (esperaba <3 ms)", name, d)
		}
	}
}
