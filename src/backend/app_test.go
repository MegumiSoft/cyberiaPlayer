package backend

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"musicdl/src/backend/queue"
	"musicdl/src/backend/ytdlp"
)

func testApp(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "cfg"))
	t.Setenv("AppData", filepath.Join(dir, "cfg"))
	a := NewApp()
	a.ctx = context.Background()
	return a
}

func TestSplitArtists(t *testing.T) {
	got := splitArtists(" A ,B,, C ")
	if strings.Join(got, "|") != "A|B|C" || len(splitArtists("")) != 0 {
		t.Errorf("%v", got)
	}
}

func TestSaveSettingsNeverOverwritesSpotifySession(t *testing.T) {
	a := testApp(t)
	out := filepath.Join(t.TempDir(), "musica")
	cur := a.settings.Get()
	cur.OutDir, cur.SpotifyClientID, cur.SpotifyRefreshToken, cur.SpotifyUser = out, "cid", "refresh-secreto", "Santi"
	if _, err := a.settings.Set(cur); err != nil {
		t.Fatal(err)
	}

	// La interfaz nunca recibe el token...
	if got := a.GetSettings(); got.SpotifyRefreshToken != "" || got.SpotifyUser != "Santi" {
		t.Errorf("GetSettings expone el token o pierde el usuario: %+v", got)
	}
	// ...así que al guardar otro ajuste lo envía vacío, y no debe borrarlo.
	form := a.GetSettings()
	form.Format = "m4a"
	saved, err := a.SaveSettings(form)
	if err != nil {
		t.Fatal(err)
	}
	if saved.SpotifyRefreshToken != "" {
		t.Error("SaveSettings no debe devolver el token")
	}
	if a.settings.Get().SpotifyRefreshToken != "refresh-secreto" || a.settings.Get().Format != "m4a" {
		t.Errorf("el token debe conservarse: %+v", a.settings.Get())
	}
	// Un formulario malicioso o desactualizado tampoco puede cambiarlo.
	form.SpotifyRefreshToken = "impuesto"
	if _, err := a.SaveSettings(form); err != nil {
		t.Fatal(err)
	}
	if a.settings.Get().SpotifyRefreshToken != "refresh-secreto" {
		t.Error("el formulario no puede reemplazar el token")
	}
	// Cambiar el Client ID invalida la sesión (el token pertenece a la otra app).
	form.SpotifyClientID = "otro-id"
	if _, err := a.SaveSettings(form); err != nil {
		t.Fatal(err)
	}
	if s := a.settings.Get(); s.SpotifyRefreshToken != "" || s.SpotifyUser != "" || s.SpotifyClientID != "otro-id" {
		t.Errorf("al cambiar el Client ID debe cerrarse la sesión: %+v", s)
	}
}

func TestSpotifyLogoutClearsSession(t *testing.T) {
	a := testApp(t)
	cur := a.settings.Get()
	cur.SpotifyRefreshToken, cur.SpotifyUser = "rt", "Yo"
	if _, err := a.settings.Set(cur); err != nil {
		t.Fatal(err)
	}
	if err := a.SpotifyLogout(); err != nil {
		t.Fatal(err)
	}
	if s := a.settings.Get(); s.SpotifyRefreshToken != "" || s.SpotifyUser != "" {
		t.Errorf("%+v", s)
	}
}

func TestPreviewSpotifyWithoutCredentialsGivesActionableError(t *testing.T) {
	a := testApp(t)
	res := a.PreviewURLs([]string{"https://open.spotify.com/track/4uLU6hMCjMI75M1A2tKUQC", "https://open.spotify.com/artist/4uLU6hMCjMI75M1A2tKUQC"})
	if res[0].Preview != nil || !strings.Contains(res[0].Error, "CONFIG") {
		t.Errorf("sin credenciales: %+v", res[0])
	}
	if res[1].Preview != nil || res[1].Error == "" {
		t.Errorf("enlace de artista: %+v", res[1])
	}
}

// fakeYtDlp crea un ejecutable que imita `yt-dlp --flat-playlist --dump-single-json ytsearchN:...`.
func fakeYtDlp(t *testing.T, json string) *ytdlp.Client {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("el yt-dlp falso es un script de shell")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "yt-dlp")
	body := "#!/bin/sh\ncat <<'EOF'\n" + json + "\nEOF\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	c := ytdlp.New(t.TempDir())
	// Instala el binario falso como la copia propia de la app.
	if err := os.MkdirAll(c.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(script, filepath.Join(c.Dir, "yt-dlp")); err != nil {
		t.Fatal(err)
	}
	return c
}

const searchJSON = `{"_type":"playlist","title":"ytsearch8:q","entries":[
 {"id":"v1","title":"Daft Punk - One More Time (Live at Coachella)","url":"https://www.youtube.com/watch?v=live","duration":318,"channel":"Fan"},
 {"id":"v2","title":"One More Time","url":"https://www.youtube.com/watch?v=topic","duration":320,"channel":"Daft Punk - Topic"},
 {"id":"v3","title":"Otra cosa","url":"https://www.youtube.com/watch?v=otra","duration":100,"channel":"X"}]}`

func TestResolverPicksBestSearchResult(t *testing.T) {
	r := ytResolver{yt: fakeYtDlp(t, searchJSON)}
	u, err := r.Resolve(context.Background(), queue.ResolveRequest{Title: "One More Time", Artist: "Daft Punk", Duration: 320})
	if err != nil || u != "https://www.youtube.com/watch?v=topic" {
		t.Errorf("u=%q err=%v", u, err)
	}
}

func TestResolverFailsInsteadOfGuessing(t *testing.T) {
	r := ytResolver{yt: fakeYtDlp(t, searchJSON)}
	_, err := r.Resolve(context.Background(), queue.ResolveRequest{Title: "Canción Inexistente", Artist: "Nadie", Duration: 200})
	if err == nil || !strings.Contains(err.Error(), "sin coincidencia clara") {
		t.Errorf("err = %v", err)
	}
	r2 := ytResolver{yt: fakeYtDlp(t, `{"_type":"playlist","entries":[]}`)}
	if _, err := r2.Resolve(context.Background(), queue.ResolveRequest{Title: "X", Artist: "Y"}); err == nil {
		t.Error("una búsqueda sin resultados debe dar error")
	}
}

func TestCopyTrackKeepsOriginalAndMeta(t *testing.T) {
	a := testApp(t)
	root := t.TempDir()
	cur := a.settings.Get()
	cur.OutDir = root
	if _, err := a.settings.Set(cur); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(root, "A"), 0o755)
	os.MkdirAll(filepath.Join(root, "B"), 0o755)
	os.WriteFile(filepath.Join(root, "A", "uno.mp3"), []byte("audio-uno"), 0o644)
	os.WriteFile(filepath.Join(root, "A", "dos.flac"), []byte("audio-dos"), 0o644)
	m := a.meta.All()["A/uno.mp3"]
	m.GridSet, m.GridBPM, m.GridOffset = true, 124, 0.5
	if err := a.meta.Set("A/uno.mp3", m); err != nil {
		t.Fatal(err)
	}

	nuevo, err := a.CopyTrack("A/uno.mp3", "B")
	if err != nil || nuevo != "B/uno.mp3" {
		t.Fatalf("copiar: %q %v", nuevo, err)
	}
	for _, p := range []string{"A/uno.mp3", "B/uno.mp3"} {
		if b, err := os.ReadFile(filepath.Join(root, p)); err != nil || string(b) != "audio-uno" {
			t.Fatalf("%s: %v", p, err)
		}
	}
	if got := a.meta.All()["B/uno.mp3"]; !got.GridSet || got.GridBPM != 124 {
		t.Fatalf("la copia debe heredar la rejilla: %+v", got)
	}
	if a.meta.All()["A/uno.mp3"].GridBPM != 124 {
		t.Fatal("el original perdió su rejilla")
	}
	// No sobrescribe ni copia sobre sí misma.
	if _, err := a.CopyTrack("A/uno.mp3", "B"); err == nil {
		t.Fatal("debió negarse a sobrescribir")
	}
	if _, err := a.CopyTrack("A/uno.mp3", "A"); err == nil {
		t.Fatal("debió negarse a copiar sobre sí misma")
	}
	if _, err := a.CopyTrack("../fuera.mp3", "B"); err == nil {
		t.Fatal("debió negarse fuera de la biblioteca")
	}

	// Set → playlist nueva: copias numeradas en el orden del set; los originales siguen.
	os.WriteFile(filepath.Join(root, "B", "07 - tres.mp3"), []byte("audio-tres"), 0o644)
	res, err := a.CreatePlaylistFromSet("Mi set: viernes", []string{"B/07 - tres.mp3", "A/dos.flac", "A/uno.mp3"})
	if err != nil || res.Copied != 3 || len(res.Skipped) != 0 {
		t.Fatalf("set: %+v %v", res, err)
	}
	ents, _ := os.ReadDir(filepath.Join(root, res.Folder))
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}
	if strings.Join(names, "|") != "01 - tres.mp3|02 - dos.flac|03 - uno.mp3" {
		t.Fatalf("nombres: %v", names)
	}
	if _, err := os.Stat(filepath.Join(root, "A", "dos.flac")); err != nil {
		t.Fatal("el original desapareció")
	}
	if _, err := a.CreatePlaylistFromSet("Mi set: viernes", []string{"A/uno.mp3"}); err == nil {
		t.Fatal("debió negarse: la playlist ya existe")
	}
	if _, err := a.CreatePlaylistFromSet("Vacío", nil); err == nil {
		t.Fatal("un set vacío no crea playlist")
	}
}

func TestConvertTracks(t *testing.T) {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg no está instalado")
	}
	a := testApp(t)
	root := t.TempDir()
	cur := a.settings.Get()
	cur.OutDir = root
	cur.FFmpegPath = ff
	if _, err := a.settings.Set(cur); err != nil {
		t.Fatal(err)
	}
	a.yt.SetFFmpegPath(ff)
	os.MkdirAll(filepath.Join(root, "A"), 0o755)
	wav := filepath.Join(root, "A", "uno.wav")
	if out, err := exec.Command(ff, "-v", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=2:sample_rate=44100", "-y", wav).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	if out, err := exec.Command(ff, "-v", "error", "-i", wav, "-y", filepath.Join(root, "A", "dos.mp3")).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	m := a.meta.All()["A/uno.wav"]
	m.GridSet, m.GridBPM, m.GridOffset = true, 128, 0.25
	if err := a.meta.Set("A/uno.wav", m); err != nil {
		t.Fatal(err)
	}
	both := []string{"A/uno.wav", "A/dos.mp3"}

	// conservar: el nuevo queda al lado, lo que ya es MP3 se omite
	res, err := a.ConvertTracks(ConvertRequest{Rels: both, Format: "mp3", Mode: "keep"})
	if err != nil || res.Converted != 1 || len(res.Skipped) != 1 || len(res.Failed) != 0 {
		t.Fatalf("keep: %+v %v", res, err)
	}
	if _, err := os.Stat(wav); err != nil {
		t.Fatal("keep: el original debe seguir")
	}
	if g := a.meta.All()["A/uno.mp3"]; !g.GridSet || g.GridBPM != 128 {
		t.Fatalf("keep: el convertido debe heredar la rejilla: %+v", g)
	}
	// otra vez: el destino ya existe → se omite, nunca se sobrescribe
	res, _ = a.ConvertTracks(ConvertRequest{Rels: []string{"A/uno.wav"}, Format: "mp3", Mode: "keep"})
	if res.Converted != 0 || len(res.Skipped) != 1 {
		t.Fatalf("keep repetido: %+v", res)
	}

	// playlist nueva: copias convertidas en otra carpeta, originales intactos
	res, err = a.ConvertTracks(ConvertRequest{Rels: both, Format: "flac", Mode: "playlist", NewName: "Flacs"})
	if err != nil || res.Converted != 2 || res.Folder != "Flacs" {
		t.Fatalf("playlist: %+v %v", res, err)
	}
	for _, p := range []string{"Flacs/uno.flac", "Flacs/dos.flac", "A/uno.wav", "A/dos.mp3"} {
		if _, err := os.Stat(filepath.Join(root, p)); err != nil {
			t.Fatalf("playlist: falta %s", p)
		}
	}
	if _, err := a.ConvertTracks(ConvertRequest{Rels: both, Format: "flac", Mode: "playlist", NewName: "Flacs"}); err == nil {
		t.Fatal("playlist: debió negarse a reutilizar una carpeta existente")
	}
	// si nada se convierte no queda carpeta vacía
	res, _ = a.ConvertTracks(ConvertRequest{Rels: []string{"A/dos.mp3"}, Format: "mp3", Mode: "playlist", NewName: "Vacia"})
	if res.Converted != 0 || res.Folder != "" {
		t.Fatalf("vacía: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(root, "Vacia")); err == nil {
		t.Fatal("no debe quedar la carpeta vacía")
	}

	// reemplazar: el original se va a la papelera
	res, err = a.ConvertTracks(ConvertRequest{Rels: []string{"A/uno.wav"}, Format: "m4a", Mode: "replace"})
	if err != nil || res.Converted != 1 || res.Trashed != 1 || len(res.Failed) != 0 {
		t.Fatalf("replace: %+v %v", res, err)
	}
	if _, err := os.Stat(wav); err == nil {
		t.Fatal("replace: el original debió irse")
	}
	if _, err := os.Stat(filepath.Join(root, "A", "uno.m4a")); err != nil {
		t.Fatal("replace: falta el convertido")
	}
	if _, ok := a.meta.All()["A/uno.wav"]; ok {
		t.Fatal("replace: los metadatos del original deben limpiarse")
	}

	// validaciones
	for _, bad := range []ConvertRequest{
		{Rels: both, Format: "xyz", Mode: "keep"},
		{Rels: both, Format: "mp3", Mode: "borrar"},
		{Rels: nil, Format: "mp3", Mode: "keep"},
	} {
		if _, err := a.ConvertTracks(bad); err == nil {
			t.Fatalf("debió rechazar %+v", bad)
		}
	}
	if res, _ := a.ConvertTracks(ConvertRequest{Rels: []string{"../fuera.wav"}, Format: "mp3", Mode: "keep"}); res.Converted != 0 || len(res.Failed) != 1 {
		t.Fatalf("ruta fuera de la biblioteca: %+v", res)
	}
}
