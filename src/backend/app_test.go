package backend

import (
	"context"
	"os"
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
