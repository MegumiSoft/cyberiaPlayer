package ytdlp

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAssetName(t *testing.T) {
	cases := []struct{ os, arch, want string }{
		{"windows", "amd64", "yt-dlp.exe"},
		{"windows", "arm64", "yt-dlp_arm64.exe"},
		{"darwin", "arm64", "yt-dlp_macos"},
		{"darwin", "amd64", "yt-dlp_macos"},
		{"linux", "amd64", "yt-dlp_linux"},
		{"linux", "arm64", "yt-dlp_linux_aarch64"},
	}
	for _, c := range cases {
		got, err := assetName(c.os, c.arch)
		if err != nil || got != c.want {
			t.Errorf("assetName(%s,%s) = %q, %v; want %q", c.os, c.arch, got, err, c.want)
		}
	}
	if _, err := assetName("plan9", "amd64"); err == nil {
		t.Error("esperaba error para un sistema no soportado")
	}
}

func TestSanitizeName(t *testing.T) {
	cases := map[string]string{
		`kawaii <3: "best" / mix?`: `kawaii <3_ _best_ _ mix_`,
		"  hola...  ":              "hola",
		"":                         "Descargas",
		"???":                      "___",
		"日本語のプレイリスト":               "日本語のプレイリスト",
	}
	for in, want := range cases {
		in2 := strings.ReplaceAll(in, "<3", "_3") // '<' también se sustituye
		want2 := strings.ReplaceAll(want, "<3", "_3")
		if got := SanitizeName(in2); got != want2 {
			t.Errorf("SanitizeName(%q) = %q; want %q", in, got, want2)
		}
	}
	long := strings.Repeat("a", 500)
	if n := len([]rune(SanitizeName(long))); n != 120 {
		t.Errorf("longitud = %d; want 120", n)
	}
}

func TestParseProgressLine(t *testing.T) {
	p, ok := parseProgressLine("mdl:dl 500 1000 NA NA NA 2048.5 7")
	if !ok || p.Phase != "downloading" || p.Percent != 50 || p.Speed != 2048.5 || p.ETA != 7 {
		t.Errorf("descarga con total: %+v ok=%v", p, ok)
	}
	p, ok = parseProgressLine("mdl:dl 500 NA 2000 NA NA NA NA")
	if !ok || p.Percent != 25 || p.ETA != -1 || p.Speed != 0 {
		t.Errorf("descarga con total estimado: %+v", p)
	}
	p, ok = parseProgressLine("mdl:dl 0 NA NA 3 12 NA NA")
	if !ok || p.Percent != 25 {
		t.Errorf("descarga por fragmentos: %+v", p)
	}
	p, ok = parseProgressLine("mdl:dl 5000 1000 NA NA NA 1 1")
	if !ok || p.Percent != 100 {
		t.Errorf("el porcentaje debe limitarse a 100: %+v", p)
	}
	p, ok = parseProgressLine("mdl:pp started")
	if !ok || p.Phase != "converting" {
		t.Errorf("conversión: %+v", p)
	}
	for _, line := range []string{"", "mdl:pp finished", "mdl:dl 1 2", "[download] 10%", "otra cosa"} {
		if _, ok := parseProgressLine(line); ok {
			t.Errorf("%q no debería interpretarse como progreso", line)
		}
	}
}

func TestParsePreviewPlaylist(t *testing.T) {
	data := []byte(`{
	  "_type": "playlist", "title": "kawaii 17", "uploader": "carolina",
	  "entries": [
	    {"id": "1", "title": "Uno", "url": "https://api.example.com/tracks/1", "duration": 120.5, "uploader": "a"},
	    null,
	    {"id": "3", "title": "Tres", "webpage_url": "https://example.com/tres", "thumbnails": [{"url": "x.jpg"}, {"url": "y.jpg"}]}
	  ]}`)
	p, err := parsePreview(data, "https://example.com/set")
	if err != nil {
		t.Fatal(err)
	}
	if !p.IsPlaylist || p.Title != "kawaii 17" || p.Uploader != "carolina" || len(p.Entries) != 2 {
		t.Fatalf("preview inesperado: %+v", p)
	}
	if p.Entries[0].URL != "https://api.example.com/tracks/1" || p.Entries[0].Index != 1 || p.Entries[0].Duration != 120.5 {
		t.Errorf("entrada 1: %+v", p.Entries[0])
	}
	if p.Entries[1].Index != 3 || p.Entries[1].URL != "https://example.com/tres" || p.Entries[1].Thumbnail != "y.jpg" {
		t.Errorf("entrada 3 debe conservar su posición original: %+v", p.Entries[1])
	}
}

func TestParsePreviewSingleAndEmpty(t *testing.T) {
	p, err := parsePreview([]byte(`{"id":"9","title":"Solo","webpage_url":"https://example.com/solo","uploader":"x","duration":10}`), "https://example.com/solo")
	if err != nil || p.IsPlaylist || len(p.Entries) != 1 || p.Entries[0].Title != "Solo" || p.Entries[0].Index != 0 {
		t.Errorf("track suelto: %+v err=%v", p, err)
	}
	if _, err := parsePreview([]byte(`{"_type":"playlist","title":"vacía","entries":[null]}`), "u"); err == nil {
		t.Error("una playlist sin entradas accesibles debe dar error")
	}
	if _, err := parsePreview([]byte(`no es json`), "u"); err == nil {
		t.Error("JSON inválido debe dar error")
	}
}

func TestBuildDownloadArgs(t *testing.T) {
	args := buildDownloadArgs(DownloadOptions{
		URL: "https://example.com/t", OutDir: "/tmp/100%/music", Folder: "Mi: lista?", Index: 3,
		Format: "mp3", Quality: "0", FFmpeg: "/usr/bin/ffmpeg",
	})
	joined := strings.Join(args, "\x00")
	for _, want := range []string{"--no-playlist", "--embed-thumbnail", "--ffmpeg-location\x00/usr/bin/ffmpeg", "--ignore-config", "--windows-filenames"} {
		if !strings.Contains(joined, want) {
			t.Errorf("faltan argumentos: %q", want)
		}
	}
	if args[len(args)-2] != "--" || args[len(args)-1] != "https://example.com/t" {
		t.Errorf("la URL debe ir al final tras '--': %v", args[len(args)-2:])
	}
	var tmpl string
	for i, a := range args {
		if a == "-o" {
			tmpl = args[i+1]
		}
	}
	want := filepath.Join("/tmp/100%%/music", "Mi_ lista_", "03 - %(title)s.%(ext)s")
	if tmpl != want {
		t.Errorf("plantilla = %q; want %q", tmpl, want)
	}
	wav := strings.Join(buildDownloadArgs(DownloadOptions{URL: "https://e.com/x", OutDir: "/o", Format: "wav", Quality: "0"}), " ")
	if strings.Contains(wav, "--embed-thumbnail") {
		t.Error("wav no admite carátula embebida")
	}
}

func TestBuildDownloadArgsFixedName(t *testing.T) {
	args := buildDownloadArgs(DownloadOptions{
		URL: "https://example.com/t", OutDir: "/o", Folder: "Álbum", Index: 2, Name: `Artista - 100% Canción: "x"?`,
		Format: "mp3", Quality: "0",
	})
	var tmpl string
	for i, a := range args {
		if a == "-o" {
			tmpl = args[i+1]
		}
	}
	want := filepath.Join("/o", "Álbum", "02 - Artista - 100%% Canción_ _x__.%(ext)s")
	if tmpl != want {
		t.Errorf("plantilla = %q; want %q", tmpl, want)
	}
}

func TestParseSearchResultsKeepDuration(t *testing.T) {
	data := []byte(`{"_type":"playlist","title":"ytsearch5:q","entries":[
	  {"id":"a","title":"Uno","url":"https://www.youtube.com/watch?v=a","duration":200,"channel":"Canal - Topic"},
	  {"id":"b","title":"Dos","url":"https://www.youtube.com/watch?v=b","duration":null}]}`)
	p, err := parsePreview(data, "")
	if err != nil || len(p.Entries) != 2 {
		t.Fatalf("p=%+v err=%v", p, err)
	}
	if p.Entries[0].Duration != 200 || p.Entries[0].Uploader != "Canal - Topic" || p.Entries[1].Duration != 0 {
		t.Errorf("entries: %+v", p.Entries)
	}
}

func TestSearchRejectsEmptyQuery(t *testing.T) {
	if _, err := New(t.TempDir()).Search(context.Background(), "   ", 5); err == nil {
		t.Error("una búsqueda vacía debe dar error")
	}
}

func TestErrorMessage(t *testing.T) {
	if got := errorMessage("WARNING: x\nERROR: [soundcloud] 123: Track not found\n"); got != "[soundcloud] 123: Track not found" {
		t.Errorf("got %q", got)
	}
	if got := errorMessage("ERROR: Postprocessing: ffprobe and ffmpeg not found. Please install"); !strings.Contains(got, "ffmpeg no encontrado") {
		t.Errorf("got %q", got)
	}
	if got := errorMessage(""); got == "" {
		t.Error("el mensaje no debe quedar vacío")
	}
}

func TestValidateURL(t *testing.T) {
	for _, ok := range []string{"https://soundcloud.com/a/sets/b", "http://127.0.0.1:8080/x.mp3"} {
		if err := validateURL(ok); err != nil {
			t.Errorf("%q debería ser válida: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "--exec=rm", "file:///etc/passwd", "soundcloud.com/x", "javascript:alert(1)"} {
		if err := validateURL(bad); err == nil {
			t.Errorf("%q debería rechazarse", bad)
		}
	}
}

func TestInstallFromServer(t *testing.T) {
	asset, err := assetName(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Skip(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/"+asset {
			http.NotFound(w, r)
			return
		}
		io.WriteString(w, "#!/bin/sh\necho 1999.01.01\n")
	}))
	defer srv.Close()
	old := DownloadBase
	DownloadBase = srv.URL + "/"
	defer func() { DownloadBase = old }()

	c := New(filepath.Join(t.TempDir(), "bin"))
	if err := c.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(c.managedPath())
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && st.Mode()&0o111 == 0 {
		t.Error("el binario instalado debe ser ejecutable")
	}
	if p, managed, err := c.Resolve(); err != nil || !managed || p != c.managedPath() {
		t.Errorf("Resolve = %q managed=%v err=%v", p, managed, err)
	}
	// Un 404 del servidor debe dar error y no dejar archivos temporales.
	DownloadBase = srv.URL + "/nada/"
	c2 := New(filepath.Join(t.TempDir(), "bin2"))
	if err := c2.Install(context.Background()); err == nil {
		t.Error("esperaba error con HTTP 404")
	}
	if entries, _ := os.ReadDir(c2.Dir); len(entries) != 0 {
		t.Errorf("quedaron archivos tras fallar: %v", entries)
	}
}

func TestResolveNotInstalled(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	c := New(t.TempDir())
	if _, _, err := c.Resolve(); runtime.GOOS == "linux" && !errors.Is(err, ErrNotInstalled) {
		// En Linux solo se busca en PATH y en rutas del sistema; si el equipo tiene yt-dlp en
		// /usr/local/bin el test no puede afirmar nada.
		if _, statErr := os.Stat("/usr/local/bin/yt-dlp"); statErr != nil {
			t.Errorf("esperaba ErrNotInstalled, got %v", err)
		}
	}
}

// ---------------------------------------------------------------------------
// Pruebas de integración: usan yt-dlp y ffmpeg reales contra un servidor local.
// Se omiten si las herramientas no están instaladas.
// ---------------------------------------------------------------------------

func integrationClient(t *testing.T) *Client {
	t.Helper()
	yt, err := exec.LookPath("yt-dlp")
	if err != nil {
		t.Skip("yt-dlp no instalado")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg no instalado")
	}
	c := New(t.TempDir())
	c.binOverride = yt
	return c
}

// serveTone genera un WAV de prueba y lo sirve por HTTP en localhost.
func serveTone(t *testing.T, throttle bool) (baseURL string) {
	t.Helper()
	dir := t.TempDir()
	wav := filepath.Join(dir, "song.wav")
	out, err := exec.Command("ffmpeg", "-loglevel", "error", "-f", "lavfi",
		"-i", "sine=frequency=440:duration=3", "-ac", "2", wav).CombinedOutput()
	if err != nil {
		t.Fatalf("ffmpeg: %v\n%s", err, out)
	}
	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.Dir(dir)))
	if throttle {
		mux.HandleFunc("/slow.wav", func(w http.ResponseWriter, r *http.Request) {
			f, err := os.Open(wav)
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			defer f.Close()
			st, _ := f.Stat()
			w.Header().Set("Content-Type", "audio/wav")
			w.Header().Set("Content-Length", itoa(st.Size()*40)) // promete 40x lo que envía de verdad
			buf := make([]byte, 16*1024)
			for {
				n, rerr := f.Read(buf)
				if n > 0 {
					if _, werr := w.Write(buf[:n]); werr != nil {
						return
					}
					if fl, ok := w.(http.Flusher); ok {
						fl.Flush()
					}
					time.Sleep(60 * time.Millisecond)
				}
				if rerr != nil {
					// sigue "enviando" lento hasta que el cliente se vaya
					for r.Context().Err() == nil {
						time.Sleep(100 * time.Millisecond)
					}
					return
				}
			}
		})
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

func itoa(n int64) string {
	const digits = "0123456789"
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = digits[n%10]
		n /= 10
	}
	return string(b[i:])
}

func TestIntegrationDownloadAndPreview(t *testing.T) {
	c := integrationClient(t)
	base := serveTone(t, false)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	pv, err := c.Preview(ctx, base+"/song.wav")
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if pv.IsPlaylist || len(pv.Entries) != 1 || pv.Entries[0].Title != "song" {
		t.Errorf("preview inesperado: %+v", pv)
	}

	out := t.TempDir()
	var mu sync.Mutex
	phases := map[string]int{}
	res, err := c.Download(ctx, DownloadOptions{
		URL: base + "/song.wav", OutDir: out, Folder: "Mi Playlist: 100%", Index: 3, Format: "mp3", Quality: "192K",
	}, func(p Progress) {
		mu.Lock()
		phases[p.Phase]++
		mu.Unlock()
	})
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	want := filepath.Join(out, "Mi Playlist_ 100%", "03 - song.mp3")
	if res.Path != want {
		t.Errorf("ruta = %q; want %q", res.Path, want)
	}
	st, err := os.Stat(res.Path)
	if err != nil || st.Size() < 1000 {
		t.Fatalf("el MP3 no existe o está vacío: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if phases["downloading"] == 0 {
		t.Errorf("no llegó ningún evento de descarga: %v", phases)
	}
	if phases["converting"] == 0 {
		t.Errorf("no llegó el evento de conversión: %v", phases)
	}
}

func TestIntegrationDownloadError(t *testing.T) {
	c := integrationClient(t)
	base := serveTone(t, false)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	_, err := c.Download(ctx, DownloadOptions{URL: base + "/no-existe.wav", OutDir: t.TempDir(), Format: "mp3", Quality: "0"}, nil)
	if err == nil || err.Error() == "" {
		t.Fatalf("esperaba un error legible, got %v", err)
	}
	t.Logf("mensaje de error: %s", err)
}

func TestIntegrationCancel(t *testing.T) {
	c := integrationClient(t)
	base := serveTone(t, true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	started := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		_, err := c.Download(ctx, DownloadOptions{URL: base + "/slow.wav", OutDir: t.TempDir(), Format: "mp3", Quality: "0"}, func(p Progress) {
			select {
			case started <- struct{}{}:
			default:
			}
		})
		done <- err
	}()
	select {
	case <-started:
	case err := <-done:
		t.Fatalf("terminó antes de poder cancelar: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("la descarga nunca empezó")
	}
	t0 := time.Now()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v; want context.Canceled", err)
		}
		t.Logf("cancelación efectiva en %v", time.Since(t0))
	case <-time.After(20 * time.Second):
		t.Fatal("cancelar no terminó el proceso a tiempo")
	}
}
