package ytdlp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestYouTubeID(t *testing.T) {
	for in, want := range map[string]string{
		"https://www.youtube.com/watch?v=dQw4w9WgXcQ":       "dQw4w9WgXcQ",
		"https://youtu.be/dQw4w9WgXcQ?t=3":                  "dQw4w9WgXcQ",
		"https://music.youtube.com/watch?v=dQw4w9WgXcQ&x=1": "dQw4w9WgXcQ",
		"https://www.youtube.com/shorts/dQw4w9WgXcQ":        "dQw4w9WgXcQ",
		"https://soundcloud.com/a/b":                        "",
		"https://evil.com/?v=dQw4w9WgXcQ":                   "",
	} {
		if got := YouTubeID(in); got != want {
			t.Errorf("%s -> %q, quería %q", in, got, want)
		}
	}
}

func TestPreviewFallsBackToOEmbedWithWarning(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"title":"Mi canción","author_name":"Canal"}`))
	}))
	defer srv.Close()
	old := OEmbedBase
	OEmbedBase = srv.URL
	defer func() { OEmbedBase = old }()

	dir := t.TempDir()
	fake := filepath.Join(dir, "yt-dlp")
	os.WriteFile(fake, []byte("#!/bin/sh\necho 'ERROR: [youtube] x: Sign in to confirm you are not a bot' >&2\nexit 1\n"), 0o755)
	c := New(dir)
	c.binOverride = fake
	p, err := c.Preview(context.Background(), "https://www.youtube.com/watch?v=dQw4w9WgXcQ")
	if err != nil {
		t.Fatal(err)
	}
	if p.Title != "Mi canción" || !strings.Contains(p.Entries[0].Thumbnail, "i.ytimg.com/vi/dQw4w9WgXcQ") {
		t.Errorf("%+v", p)
	}
	if !strings.Contains(p.Warning, "not a bot") {
		t.Errorf("el aviso debe traer el motivo real: %q", p.Warning)
	}
	// Una URL que no es de YouTube no se disimula: se devuelve el error.
	if _, err := c.Preview(context.Background(), "https://soundcloud.com/a/b"); err == nil || !strings.Contains(err.Error(), "not a bot") {
		t.Errorf("err=%v", err)
	}
}
