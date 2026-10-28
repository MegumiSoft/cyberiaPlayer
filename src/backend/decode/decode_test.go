package decode

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestToPCMFlacAndWav(t *testing.T) {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg no está instalado")
	}
	dir := t.TempDir()
	for _, ext := range []string{"wav", "flac", "aiff"} {
		in := filepath.Join(dir, "t."+ext)
		// 3 s de tono estéreo a 44,1 kHz / 24 bits donde el formato lo permite
		args := []string{"-v", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=3:sample_rate=44100", "-ac", "2"}
		if ext == "flac" {
			args = append(args, "-sample_fmt", "s32")
		}
		if out, err := exec.Command(ff, append(args, "-y", in)...).CombinedOutput(); err != nil {
			t.Fatalf("%s: %v %s", ext, err, out)
		}
		pcm := filepath.Join(dir, ext+".pcm")
		n, err := ToPCM(context.Background(), ff, in, pcm, nil)
		if err != nil {
			t.Fatalf("%s: %v", ext, err)
		}
		want := int64(3 * SampleRate * 2)
		if n < want-2000 || n > want+2000 {
			t.Fatalf("%s: %d bytes, esperaba ~%d", ext, n, want)
		}
		st, _ := os.Stat(pcm)
		if st.Size() != n {
			t.Fatal("tamaño distinto")
		}
	}
	// archivo que no es audio: error limpio
	bad := filepath.Join(dir, "bad.wav")
	os.WriteFile(bad, []byte("no es audio"), 0o644)
	if _, err := ToPCM(context.Background(), ff, bad, filepath.Join(dir, "bad.pcm"), nil); err == nil {
		t.Fatal("debió fallar")
	}
}

func TestTooLongIsRejectedWithoutDecoding(t *testing.T) {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg no está instalado")
	}
	dir := t.TempDir()
	long := filepath.Join(dir, "mix.wav")
	// 31 min de silencio a 8 kHz mono (~30 MB): pasa de MaxSeconds
	if out, err := exec.Command(ff, "-v", "error", "-f", "lavfi", "-i", "anullsrc=r=8000:cl=mono", "-t", "1860", "-y", long).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	d, ok := Duration(context.Background(), ff, long, nil)
	if !ok || d < 1859 || d > 1861 {
		t.Fatalf("duración leída: %v ok=%v", d, ok)
	}
	pcm := filepath.Join(dir, "mix.pcm")
	if _, err := ToPCM(context.Background(), ff, long, pcm, nil); err != ErrTooLong {
		t.Fatalf("esperaba ErrTooLong, obtuve %v", err)
	}
	if _, err := os.Stat(pcm); err == nil {
		t.Fatal("no debió decodificar (ni dejar archivo) para un audio demasiado largo")
	}
	if _, ok := Duration(context.Background(), ff, filepath.Join(dir, "no-existe.wav"), nil); ok {
		t.Fatal("un archivo inexistente no tiene duración")
	}
}
