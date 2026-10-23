package normalize

import (
	"context"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

func tool(t *testing.T) Runner {
	p, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg no instalado")
	}
	return Runner{FFmpeg: p}
}

// tono genera un archivo con una onda de ruido rosa/seno al volumen dado (dB) y etiquetas.
func tono(t *testing.T, r Runner, path string, db float64) {
	args := []string{"-y", "-f", "lavfi", "-i", "sine=frequency=220:duration=6:sample_rate=44100", "-af", "volume=" + ftoa(db) + "dB", "-metadata", "title=Prueba", "-metadata", "artist=Yo"}
	switch filepath.Ext(path) {
	case ".mp3":
		args = append(args, "-c:a", "libmp3lame", "-b:a", "192k")
	case ".m4a":
		args = append(args, "-c:a", "aac", "-b:a", "192k")
	case ".flac":
		args = append(args, "-c:a", "flac")
	}
	args = append(args, path)
	if out, err := r.cmd(context.Background(), args...).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}

func ftoa(f float64) string { return strconv.FormatFloat(f, 'f', 1, 64) }

func TestMeasureAndApplyAllFormats(t *testing.T) {
	r := tool(t)
	ctx := context.Background()
	for _, ext := range []string{".mp3", ".m4a", ".flac", ".wav"} {
		t.Run(ext, func(t *testing.T) {
			dir := t.TempDir()
			quiet, ref := filepath.Join(dir, "quiet"+ext), filepath.Join(dir, "ref"+ext)
			tono(t, r, quiet, -30)
			tono(t, r, ref, -16)
			lq, err := r.Measure(ctx, quiet)
			if err != nil {
				t.Fatal(err)
			}
			lr, err := r.Measure(ctx, ref)
			if err != nil {
				t.Fatal(err)
			}
			if d := lr.LUFS - lq.LUFS; math.Abs(d-14) > 1 {
				t.Fatalf("diferencia medida %.2f, se esperaba ≈14", d)
			}
			gain := lr.LUFS - lq.LUFS
			bk := filepath.Join(dir, "bk")
			res, err := r.Apply(ctx, quiet, lq.LUFS, gain, bk)
			if err != nil {
				t.Fatal(err)
			}
			if math.Abs(res.After-lr.LUFS) > 1 {
				t.Fatalf("después %.2f LUFS, referencia %.2f", res.After, lr.LUFS)
			}
			if res.Backup == "" || res.NewSize <= 0 {
				t.Fatalf("%+v", res)
			}
			if _, err := os.Stat(res.Backup); err != nil {
				t.Fatal("falta la copia")
			}
			// el archivo final se mide igual que lo informado y conserva el título
			again, _ := r.Measure(ctx, quiet)
			if math.Abs(again.LUFS-res.After) > 0.2 {
				t.Fatalf("remedido %.2f vs %.2f", again.LUFS, res.After)
			}
			if ext != ".wav" {
				out, _ := r.cmd(ctx, "-i", quiet).CombinedOutput()
				if !contains(string(out), "Prueba") {
					t.Fatalf("se perdieron las etiquetas:\n%s", out)
				}
			}
			if entries, _ := os.ReadDir(dir); len(entries) != 4 { // quiet, ref, bk + ?
				for _, e := range entries {
					t.Log(e.Name())
				}
			}
		})
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestRefusesHugeGain(t *testing.T) {
	r := Runner{FFmpeg: "/nonexistent"}
	if _, err := r.Apply(context.Background(), "x.mp3", -40, 30, ""); err == nil {
		t.Fatal("debía rechazar +30 dB")
	}
}
