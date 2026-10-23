package spotify

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseRef(t *testing.T) {
	ok := map[string]Ref{
		"https://open.spotify.com/track/4uLU6hMCjMI75M1A2tKUQC?si=abc":  {"track", "4uLU6hMCjMI75M1A2tKUQC"},
		"https://open.spotify.com/intl-es/album/1A2GTWGtFfWp7KSQTwWOyo": {"album", "1A2GTWGtFfWp7KSQTwWOyo"},
		"  https://open.spotify.com/playlist/37i9dQZF1DXcBWIGoYBM5M/  ": {"playlist", "37i9dQZF1DXcBWIGoYBM5M"},
		"spotify:track:4uLU6hMCjMI75M1A2tKUQC":                          {"track", "4uLU6hMCjMI75M1A2tKUQC"},
		"spotify:liked":                                                 {"liked", "me"},
		"https://open.spotify.com/collection/tracks":                    {"liked", "me"},
		"http://open.spotify.com/playlist/37i9dQZF1DXcBWIGoYBM5M#algo":  {"playlist", "37i9dQZF1DXcBWIGoYBM5M"},
	}
	for in, want := range ok {
		got, found := ParseRef(in)
		if !found || got != want {
			t.Errorf("ParseRef(%q) = %+v, %v; want %+v", in, got, found, want)
		}
	}
	for _, bad := range []string{"", "https://open.spotify.com/artist/4uLU6hMCjMI75M1A2tKUQC", "https://evil.com/open.spotify.com/track/4uLU6hMCjMI75M1A2tKUQC",
		"https://open.spotify.com/track/short", "spotify:artist:4uLU6hMCjMI75M1A2tKUQC"} {
		if _, found := ParseRef(bad); found {
			t.Errorf("ParseRef(%q) no debería reconocerse", bad)
		}
	}
	if !IsSpotifyURL("https://open.spotify.com/artist/x") || !IsSpotifyURL("spotify:artist:x") || IsSpotifyURL("https://soundcloud.com/a") {
		t.Error("IsSpotifyURL")
	}
}

func TestChallengeRFC7636Vector(t *testing.T) {
	// Ejemplo de la RFC 7636, apéndice B.
	if got := Challenge("dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"); got != "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM" {
		t.Errorf("challenge = %s", got)
	}
}

type fakeSpotify struct {
	srv       *httptest.Server
	tokenHits int32
	apiHits   int32
	handlers  map[string]http.HandlerFunc
}

func newFake(t *testing.T) (*fakeSpotify, *Client) {
	t.Helper()
	f := &fakeSpotify{handlers: map[string]http.HandlerFunc{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/token", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&f.tokenHits, 1)
		_ = r.ParseForm()
		w.Header().Set("Content-Type", "application/json")
		switch r.Form.Get("grant_type") {
		case "client_credentials":
			if r.Header.Get("Authorization") != "Basic "+"aWQ6c2VjcmV0" { // id:secret
				w.WriteHeader(400)
				fmt.Fprint(w, `{"error":"invalid_client","error_description":"Invalid client secret"}`)
				return
			}
			fmt.Fprint(w, `{"access_token":"cc-token","expires_in":3600}`)
		case "refresh_token":
			if r.Form.Get("refresh_token") != "good-refresh" {
				w.WriteHeader(400)
				fmt.Fprint(w, `{"error":"invalid_grant","error_description":"Refresh token revoked"}`)
				return
			}
			fmt.Fprint(w, `{"access_token":"user-token","expires_in":3600}`)
		case "authorization_code":
			if r.Form.Get("code") != "the-code" || r.Form.Get("code_verifier") == "" || r.Form.Get("client_id") != "id" {
				w.WriteHeader(400)
				fmt.Fprint(w, `{"error":"invalid_grant"}`)
				return
			}
			fmt.Fprint(w, `{"access_token":"user-token","expires_in":3600,"refresh_token":"good-refresh"}`)
		default:
			w.WriteHeader(400)
		}
	})
	mux.HandleFunc("/v1/", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&f.apiHits, 1)
		w.Header().Set("Content-Type", "application/json")
		if h, ok := f.handlers[r.URL.Path]; ok {
			h(w, r)
			return
		}
		w.WriteHeader(404)
		fmt.Fprint(w, `{"error":{"status":404,"message":"Not found"}}`)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	c := New()
	c.AuthBase = f.srv.URL
	c.APIBase = f.srv.URL + "/v1"
	return f, c
}

var ccCreds = Creds{ClientID: "id", ClientSecret: "secret"}
var userCreds = Creds{ClientID: "id", RefreshToken: "good-refresh"}

func TestFetchTrack(t *testing.T) {
	f, c := newFake(t)
	f.handlers["/v1/tracks/4uLU6hMCjMI75M1A2tKUQC"] = func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer cc-token" {
			w.WriteHeader(401)
			return
		}
		fmt.Fprint(w, `{"id":"4uLU6hMCjMI75M1A2tKUQC","name":"Canción","type":"track","duration_ms":201000,
		  "artists":[{"name":"A"},{"name":"B"}],"album":{"name":"Disco","images":[{"url":"http://img/1.jpg"}]}}`)
	}
	col, err := c.Fetch(context.Background(), ccCreds, "https://open.spotify.com/track/4uLU6hMCjMI75M1A2tKUQC")
	if err != nil {
		t.Fatal(err)
	}
	if col.Kind != "track" || len(col.Tracks) != 1 {
		t.Fatalf("%+v", col)
	}
	tr := col.Tracks[0]
	if tr.Title != "Canción" || tr.Artist() != "A, B" || tr.DurationMs != 201000 || tr.Album != "Disco" || tr.Cover != "http://img/1.jpg" || tr.Number != 0 {
		t.Errorf("track: %+v", tr)
	}
	// El token de Client Credentials se reutiliza.
	if _, err := c.Fetch(context.Background(), ccCreds, "spotify:track:4uLU6hMCjMI75M1A2tKUQC"); err != nil {
		t.Fatal(err)
	}
	if n := atomic.LoadInt32(&f.tokenHits); n != 1 {
		t.Errorf("peticiones de token = %d; want 1 (debe cachearse)", n)
	}
}

func TestFetchAlbumPaginates(t *testing.T) {
	f, c := newFake(t)
	f.handlers["/v1/albums/1A2GTWGtFfWp7KSQTwWOyo"] = func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"name":"Álbum","images":[{"url":"cover"}],"artists":[{"name":"Banda"}],
		  "tracks":{"items":[
		    {"id":"t1","name":"Uno","duration_ms":1000,"track_number":1,"artists":[{"name":"Banda"}]},
		    {"id":"","name":"Sin id","artists":[]}],
		   "next":"%s/v1/albums/1A2GTWGtFfWp7KSQTwWOyo/tracks?offset=2"}}`, f.srv.URL)
	}
	f.handlers["/v1/albums/1A2GTWGtFfWp7KSQTwWOyo/tracks"] = func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("offset") != "2" {
			w.WriteHeader(500)
			return
		}
		fmt.Fprint(w, `{"items":[{"id":"t3","name":"Tres","duration_ms":3000,"track_number":3,"artists":[{"name":"Banda"}]}],"next":null}`)
	}
	col, err := c.Fetch(context.Background(), ccCreds, "https://open.spotify.com/album/1A2GTWGtFfWp7KSQTwWOyo")
	if err != nil {
		t.Fatal(err)
	}
	if col.Name != "Álbum" || col.Owner != "Banda" || len(col.Tracks) != 2 || col.Skipped != 1 {
		t.Fatalf("%+v", col)
	}
	if col.Tracks[0].Album != "Álbum" || col.Tracks[0].Cover != "cover" {
		t.Errorf("la pista hereda álbum y portada: %+v", col.Tracks[0])
	}
	if col.Tracks[1].Title != "Tres" || col.Tracks[1].Number != 3 {
		t.Errorf("la numeración debe contar la pista omitida: %+v", col.Tracks[1])
	}
}

func playlistMeta(w http.ResponseWriter) {
	fmt.Fprint(w, `{"name":"Mi lista","images":[],"owner":{"display_name":"Yo"}}`)
}

func TestPlaylistNewAndOldShapes(t *testing.T) {
	f, c := newFake(t)
	f.handlers["/v1/playlists/37i9dQZF1DXcBWIGoYBM5M"] = func(w http.ResponseWriter, r *http.Request) { playlistMeta(w) }
	// Formato nuevo: "item" es el objeto y "track" pasó a ser un booleano.
	f.handlers["/v1/playlists/37i9dQZF1DXcBWIGoYBM5M/items"] = func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer user-token" {
			w.WriteHeader(401)
			return
		}
		fmt.Fprint(w, `{"items":[
		  {"track":true,"item":{"id":"a1","name":"Nueva","type":"track","duration_ms":100000,"artists":[{"name":"X"}],"album":{"name":"Al","images":[]}}},
		  {"track":true,"item":{"id":"ep1","name":"Podcast","type":"episode"}},
		  {"track":true,"item":null},
		  {"track":true,"item":{"id":"loc","name":"Local","is_local":true,"type":"track"}},
		  {"track":{"id":"a2","name":"Vieja","type":"track","duration_ms":90000,"artists":[{"name":"Y"}],"album":{"name":"Al2","images":[]}}}
		],"next":null}`)
	}
	col, err := c.Fetch(context.Background(), userCreds, "https://open.spotify.com/playlist/37i9dQZF1DXcBWIGoYBM5M")
	if err != nil {
		t.Fatal(err)
	}
	if col.Name != "Mi lista" || col.Owner != "Yo" || len(col.Tracks) != 2 || col.Skipped != 3 {
		t.Fatalf("%+v", col)
	}
	if col.Tracks[0].Title != "Nueva" || col.Tracks[1].Title != "Vieja" || col.Tracks[1].Number != 5 {
		t.Errorf("tracks: %+v", col.Tracks)
	}
}

func TestPlaylistNeedsLoginWithClientCredentials(t *testing.T) {
	_, c := newFake(t)
	_, err := c.Fetch(context.Background(), ccCreds, "https://open.spotify.com/playlist/37i9dQZF1DXcBWIGoYBM5M")
	if err == nil || !strings.Contains(err.Error(), "iniciar sesión") {
		t.Errorf("err = %v", err)
	}
}

func TestPlaylistForbiddenIsFriendly(t *testing.T) {
	f, c := newFake(t)
	f.handlers["/v1/playlists/37i9dQZF1DXcBWIGoYBM5M"] = func(w http.ResponseWriter, r *http.Request) { playlistMeta(w) }
	f.handlers["/v1/playlists/37i9dQZF1DXcBWIGoYBM5M/items"] = func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		fmt.Fprint(w, `{"error":{"status":403,"message":"Forbidden"}}`)
	}
	_, err := c.Fetch(context.Background(), userCreds, "https://open.spotify.com/playlist/37i9dQZF1DXcBWIGoYBM5M")
	if err == nil || !strings.Contains(err.Error(), "posees o en las que colaboras") {
		t.Errorf("err = %v", err)
	}
}

func TestBadCredentialsAndMissingFields(t *testing.T) {
	_, c := newFake(t)
	_, err := c.Fetch(context.Background(), Creds{ClientID: "id", ClientSecret: "mal"}, "spotify:track:4uLU6hMCjMI75M1A2tKUQC")
	if err == nil || !strings.Contains(err.Error(), "rechazó las credenciales") {
		t.Errorf("err = %v", err)
	}
	if _, err := c.Fetch(context.Background(), Creds{}, "spotify:track:4uLU6hMCjMI75M1A2tKUQC"); err != ErrNoCredentials {
		t.Errorf("err = %v", err)
	}
	if _, err := c.Fetch(context.Background(), Creds{ClientID: "id"}, "spotify:track:4uLU6hMCjMI75M1A2tKUQC"); err == nil || !strings.Contains(err.Error(), "Client Secret") {
		t.Errorf("err = %v", err)
	}
	if _, err := c.Fetch(context.Background(), ccCreds, "https://open.spotify.com/artist/4uLU6hMCjMI75M1A2tKUQC"); err == nil {
		t.Error("un enlace de artista no es soportado")
	}
}

func TestRevokedRefreshToken(t *testing.T) {
	_, c := newFake(t)
	_, err := c.Fetch(context.Background(), Creds{ClientID: "id", RefreshToken: "revocado"}, "spotify:track:4uLU6hMCjMI75M1A2tKUQC")
	if err == nil || !strings.Contains(err.Error(), "vuelve a iniciar sesión") {
		t.Errorf("err = %v", err)
	}
}

func TestRetryOn429(t *testing.T) {
	f, c := newFake(t)
	var calls int32
	f.handlers["/v1/tracks/4uLU6hMCjMI75M1A2tKUQC"] = func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(429)
			return
		}
		fmt.Fprint(w, `{"id":"4uLU6hMCjMI75M1A2tKUQC","name":"Ok","type":"track","artists":[{"name":"A"}]}`)
	}
	start := time.Now()
	col, err := c.Fetch(context.Background(), ccCreds, "spotify:track:4uLU6hMCjMI75M1A2tKUQC")
	if err != nil || col.Tracks[0].Title != "Ok" {
		t.Fatalf("col=%+v err=%v", col, err)
	}
	if time.Since(start) < 900*time.Millisecond {
		t.Error("debe respetar Retry-After")
	}
}

func TestTestMessages(t *testing.T) {
	f, c := newFake(t)
	f.handlers["/v1/me"] = func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"display_name":"Santi","id":"s"}`) }
	msg, err := c.Test(context.Background(), userCreds)
	if err != nil || !strings.Contains(msg, "Santi") {
		t.Errorf("%q %v", msg, err)
	}
	f.handlers["/v1/tracks/11dFghVXANMlKmJXsNCbNl"] = func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"name":"x"}`) }
	msg, err = c.Test(context.Background(), ccCreds)
	if err != nil || !strings.Contains(msg, "Credenciales correctas") {
		t.Errorf("%q %v", msg, err)
	}
}

func TestLoginFlow(t *testing.T) {
	_, c := newFake(t)
	var authURL string
	open := func(u string) {
		authURL = u
		// Simula al navegador: Spotify redirige al callback con el código y el mismo state.
		go func() {
			parsed, _ := url.Parse(u)
			q := parsed.Query()
			cb := q.Get("redirect_uri") + "?code=the-code&state=" + url.QueryEscape(q.Get("state"))
			resp, err := http.Get(cb)
			if err == nil {
				resp.Body.Close()
			}
		}()
	}
	refresh, err := c.Login(context.Background(), "id", 0, 5*time.Second, open)
	if err != nil {
		t.Fatal(err)
	}
	if refresh != "good-refresh" {
		t.Errorf("refresh = %q", refresh)
	}
	p, _ := url.Parse(authURL)
	q := p.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" || q.Get("client_id") != "id" ||
		!strings.Contains(q.Get("scope"), "playlist-read-private") || !strings.HasPrefix(q.Get("redirect_uri"), "http://127.0.0.1:") {
		t.Errorf("authorize url: %s", authURL)
	}
}

func TestLoginRejectsWrongStateAndTimeout(t *testing.T) {
	_, c := newFake(t)
	open := func(u string) {
		go func() {
			parsed, _ := url.Parse(u)
			resp, err := http.Get(parsed.Query().Get("redirect_uri") + "?code=the-code&state=otro")
			if err == nil {
				resp.Body.Close()
			}
		}()
	}
	if _, err := c.Login(context.Background(), "id", 0, 5*time.Second, open); err == nil || !strings.Contains(err.Error(), "state") {
		t.Errorf("err = %v", err)
	}
	if _, err := c.Login(context.Background(), "id", 0, 150*time.Millisecond, func(string) {}); err == nil || !strings.Contains(err.Error(), "tiempo") {
		t.Errorf("err = %v", err)
	}
	if _, err := c.Login(context.Background(), "", 0, time.Second, func(string) {}); err != ErrNoCredentials {
		t.Errorf("err = %v", err)
	}
}

func TestParseErrBody(t *testing.T) {
	cases := map[string]string{
		`{"error":{"status":401,"message":"The access token expired"}}`:          "The access token expired",
		`{"error":"invalid_client","error_description":"Invalid client secret"}`: "invalid_client: Invalid client secret",
		`no json`: "",
	}
	for body, want := range cases {
		err := parseErrBody(400, []byte(body)).(*apiError)
		if err.Msg != want {
			t.Errorf("%s -> %q; want %q", body, err.Msg, want)
		}
	}
	_ = json.Valid
}

func TestForbiddenRetriesAndShowsSpotifyMessage(t *testing.T) {
	f, c := newFake(t)
	var calls int32
	f.handlers["/v1/tracks/4uLU6hMCjMI75M1A2tKUQC"] = func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(403)
		fmt.Fprint(w, `{"error":{"status":403,"message":"Active premium subscription required for the owner of the app."}}`)
	}
	_, err := c.Fetch(context.Background(), ccCreds, "spotify:track:4uLU6hMCjMI75M1A2tKUQC")
	if err == nil || !strings.Contains(err.Error(), "Active premium subscription required") {
		t.Fatalf("falta el mensaje real de Spotify: %v", err)
	}
	if n := atomic.LoadInt32(&calls); n != 2 {
		t.Errorf("debe reintentar una vez con token nuevo, llamadas=%d", n)
	}
}
