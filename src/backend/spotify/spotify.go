// Package spotify lee metadatos (canciones, álbumes y playlists) de la API web de Spotify.
// No descarga audio: solo obtiene la lista de canciones para buscarlas después en otra fuente.
//
// Dos formas de autenticarse:
//   - Client Credentials (Client ID + Secret): sirve para canciones y álbumes.
//   - Login de usuario con PKCE (solo Client ID): necesario para playlists, porque desde febrero de
//     2026 Spotify solo devuelve el contenido de playlists que el usuario posee o en las que colabora.
package spotify

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	DefaultAuthBase = "https://accounts.spotify.com"
	DefaultAPIBase  = "https://api.spotify.com/v1"
	pageSize        = 50
	maxTracks       = 2000 // tope de seguridad por colección
)

var (
	ErrNoCredentials = errors.New("faltan las credenciales de Spotify (Client ID) en CONFIG")
	ErrNeedLogin     = errors.New("para leer playlists hace falta iniciar sesión con Spotify en CONFIG (solo se pueden leer las playlists que posees o en las que colaboras)")
)

type Track struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Artists    []string `json:"artists"`
	DurationMs int      `json:"durationMs"`
	Album      string   `json:"album"`
	Cover      string   `json:"cover"`
	Number     int      `json:"number"` // posición dentro de la colección (1-based)
	URL        string   `json:"url"`
}

// Artist devuelve los artistas unidos por coma.
func (t Track) Artist() string { return strings.Join(t.Artists, ", ") }

type Collection struct {
	Kind    string `json:"kind"` // track, album, playlist o liked
	ID      string `json:"id"`
	Name    string `json:"name"`
	Owner   string `json:"owner"`
	Cover   string `json:"cover"`
	Tracks  []Track
	Skipped int // elementos que no se pudieron usar (archivos locales, podcasts, no disponibles)
}

// Ref identifica un recurso de Spotify dentro de un enlace.
type Ref struct{ Kind, ID string }

var urlRe = regexp.MustCompile(`^https?://open\.spotify\.com/(?:intl-[a-zA-Z-]+/)?(track|album|playlist)/([A-Za-z0-9]{10,40})(?:[/?#].*)?$`)
var uriRe = regexp.MustCompile(`^spotify:(track|album|playlist):([A-Za-z0-9]{10,40})$`)

// ParseRef reconoce enlaces open.spotify.com y URIs spotify:...; también "Canciones que te gustan".
func ParseRef(raw string) (Ref, bool) {
	s := strings.TrimSpace(raw)
	if m := urlRe.FindStringSubmatch(s); m != nil {
		return Ref{m[1], m[2]}, true
	}
	if m := uriRe.FindStringSubmatch(s); m != nil {
		return Ref{m[1], m[2]}, true
	}
	if s == "spotify:liked" || strings.HasPrefix(s, "https://open.spotify.com/collection/tracks") {
		return Ref{"liked", "me"}, true
	}
	return Ref{}, false
}

// IsSpotifyURL indica si el texto parece algo de Spotify (aunque no sea un enlace válido).
func IsSpotifyURL(raw string) bool {
	s := strings.TrimSpace(raw)
	return strings.HasPrefix(s, "spotify:") || strings.Contains(s, "open.spotify.com/")
}

type Client struct {
	HTTP     *http.Client
	AuthBase string
	APIBase  string

	mu     sync.Mutex
	cc     token  // token de Client Credentials en caché
	user   token  // token de usuario (PKCE) en caché
	cacheK string // credenciales con las que se obtuvieron los tokens en caché
}

type token struct {
	access string
	expiry time.Time
}

func (t token) valid() bool {
	return t.access != "" && time.Now().Before(t.expiry.Add(-30*time.Second))
}

func New() *Client {
	return &Client{HTTP: &http.Client{Timeout: 30 * time.Second}, AuthBase: DefaultAuthBase, APIBase: DefaultAPIBase}
}

// Creds son las credenciales actuales; se piden en cada llamada para reflejar cambios en CONFIG.
type Creds struct {
	ClientID     string
	ClientSecret string
	RefreshToken string // si existe, se usa la sesión de usuario
}

func (c Creds) key() string { return c.ClientID + "\x00" + c.ClientSecret + "\x00" + c.RefreshToken }

type apiError struct {
	Status int
	Msg    string
}

func (e *apiError) Error() string {
	if e.Msg != "" {
		return fmt.Sprintf("Spotify (%d): %s", e.Status, e.Msg)
	}
	return fmt.Sprintf("Spotify respondió HTTP %d", e.Status)
}

func parseErrBody(status int, body []byte) error {
	var v struct {
		Error json.RawMessage `json:"error"`
		Desc  string          `json:"error_description"`
	}
	_ = json.Unmarshal(body, &v)
	msg := ""
	if len(v.Error) > 0 {
		var obj struct {
			Message string `json:"message"`
		}
		var str string
		if json.Unmarshal(v.Error, &obj) == nil && obj.Message != "" {
			msg = obj.Message
		} else if json.Unmarshal(v.Error, &str) == nil {
			msg = str
			if v.Desc != "" {
				msg += ": " + v.Desc
			}
		}
	}
	return &apiError{Status: status, Msg: msg}
}

// postToken llama al endpoint de tokens con un formulario.
func (c *Client) postToken(ctx context.Context, form url.Values, basicID, basicSecret string) (token, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.AuthBase+"/api/token", strings.NewReader(form.Encode()))
	if err != nil {
		return token{}, "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if basicSecret != "" {
		req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(basicID+":"+basicSecret)))
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return token{}, "", fmt.Errorf("no se pudo contactar a Spotify: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return token{}, "", parseErrBody(resp.StatusCode, body)
	}
	var v struct {
		AccessToken  string `json:"access_token"`
		ExpiresIn    int    `json:"expires_in"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.Unmarshal(body, &v); err != nil || v.AccessToken == "" {
		return token{}, "", errors.New("respuesta de token de Spotify ilegible")
	}
	return token{access: v.AccessToken, expiry: time.Now().Add(time.Duration(v.ExpiresIn) * time.Second)}, v.RefreshToken, nil
}

// accessToken devuelve un token válido. user indica si se exige sesión de usuario.
func (c *Client) accessToken(ctx context.Context, cr Creds, needUser bool) (string, error) {
	cr.ClientID = strings.TrimSpace(cr.ClientID)
	cr.ClientSecret = strings.TrimSpace(cr.ClientSecret)
	if cr.ClientID == "" {
		return "", ErrNoCredentials
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cacheK != cr.key() {
		c.cc, c.user, c.cacheK = token{}, token{}, cr.key()
	}
	if cr.RefreshToken != "" {
		if c.user.valid() {
			return c.user.access, nil
		}
		form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {cr.RefreshToken}, "client_id": {cr.ClientID}}
		t, _, err := c.postToken(ctx, form, "", "")
		if err != nil {
			return "", fmt.Errorf("la sesión de Spotify expiró, vuelve a iniciar sesión en CONFIG (%w)", err)
		}
		c.user = t
		return t.access, nil
	}
	if needUser {
		return "", ErrNeedLogin
	}
	if cr.ClientSecret == "" {
		return "", errors.New("falta el Client Secret de Spotify (o inicia sesión con Spotify) en CONFIG")
	}
	if c.cc.valid() {
		return c.cc.access, nil
	}
	t, _, err := c.postToken(ctx, url.Values{"grant_type": {"client_credentials"}}, cr.ClientID, cr.ClientSecret)
	if err != nil {
		var ae *apiError
		if errors.As(err, &ae) && ae.Status == http.StatusBadRequest {
			return "", errors.New("Spotify rechazó las credenciales: revisa el Client ID y el Client Secret")
		}
		return "", err
	}
	c.cc = t
	return t.access, nil
}

// get hace una petición autenticada y decodifica el JSON. Reintenta una vez ante un 429.
func (c *Client) get(ctx context.Context, cr Creds, needUser bool, target string, out any) error {
	tok, err := c.accessToken(ctx, cr, needUser)
	if err != nil {
		return err
	}
	authRetried := false
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := c.HTTP.Do(req)
		if err != nil {
			return fmt.Errorf("no se pudo contactar a Spotify: %w", err)
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests && attempt == 0 {
			wait := 2 * time.Second
			if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && s > 0 && s <= 10 {
				wait = time.Duration(s) * time.Second
			}
			select {
			case <-time.After(wait):
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if (resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized) && !authRetried {
			// Un token cacheado puede haber quedado viejo (p. ej. tras cambiar permisos o Premium):
			// se descarta y se pide uno nuevo antes de rendirse.
			authRetried = true
			c.mu.Lock()
			c.cc, c.user = token{}, token{}
			c.mu.Unlock()
			if tok, err = c.accessToken(ctx, cr, needUser); err != nil {
				return err
			}
			continue
		}
		if resp.StatusCode != http.StatusOK {
			return parseErrBody(resp.StatusCode, body)
		}
		if err := json.Unmarshal(body, out); err != nil {
			return fmt.Errorf("respuesta de Spotify ilegible: %w", err)
		}
		return nil
	}
}

type image struct {
	URL string `json:"url"`
}

type rawArtist struct {
	Name string `json:"name"`
}

type rawTrack struct {
	ID         string      `json:"id"`
	Name       string      `json:"name"`
	Type       string      `json:"type"`
	IsLocal    bool        `json:"is_local"`
	DurationMs int         `json:"duration_ms"`
	TrackNo    int         `json:"track_number"`
	Artists    []rawArtist `json:"artists"`
	Album      struct {
		Name   string  `json:"name"`
		Images []image `json:"images"`
	} `json:"album"`
}

func (r *rawTrack) usable() bool {
	return r != nil && r.ID != "" && !r.IsLocal && r.Name != "" && (r.Type == "" || r.Type == "track")
}

func (r *rawTrack) toTrack(albumName, albumCover string) Track {
	t := Track{ID: r.ID, Title: r.Name, DurationMs: r.DurationMs, Number: r.TrackNo, URL: "https://open.spotify.com/track/" + r.ID}
	for _, a := range r.Artists {
		if a.Name != "" {
			t.Artists = append(t.Artists, a.Name)
		}
	}
	t.Album, t.Cover = r.Album.Name, firstImage(r.Album.Images)
	if t.Album == "" {
		t.Album = albumName
	}
	if t.Cover == "" {
		t.Cover = albumCover
	}
	return t
}

func firstImage(imgs []image) string {
	if len(imgs) > 0 {
		return imgs[0].URL
	}
	return ""
}

// Fetch lee la colección a la que apunta el enlace.
func (c *Client) Fetch(ctx context.Context, cr Creds, raw string) (*Collection, error) {
	ref, ok := ParseRef(raw)
	if !ok {
		return nil, fmt.Errorf("enlace de Spotify no reconocido: %q (usa un enlace de canción, álbum o playlist)", raw)
	}
	switch ref.Kind {
	case "track":
		return c.fetchTrack(ctx, cr, ref.ID)
	case "album":
		return c.fetchAlbum(ctx, cr, ref.ID)
	case "playlist":
		return c.fetchPlaylist(ctx, cr, ref.ID)
	default:
		return c.fetchLiked(ctx, cr)
	}
}

func (c *Client) fetchTrack(ctx context.Context, cr Creds, id string) (*Collection, error) {
	var r rawTrack
	if err := c.get(ctx, cr, false, c.APIBase+"/tracks/"+id, &r); err != nil {
		return nil, friendly(err, "canción")
	}
	if !r.usable() {
		return nil, errors.New("esta canción no está disponible en Spotify")
	}
	t := r.toTrack("", "")
	t.Number = 0
	return &Collection{Kind: "track", ID: id, Name: t.Title, Owner: t.Artist(), Cover: t.Cover, Tracks: []Track{t}}, nil
}

func (c *Client) fetchAlbum(ctx context.Context, cr Creds, id string) (*Collection, error) {
	var a struct {
		Name    string      `json:"name"`
		Images  []image     `json:"images"`
		Artists []rawArtist `json:"artists"`
		Tracks  struct {
			Items []*rawTrack `json:"items"`
			Next  string      `json:"next"`
		} `json:"tracks"`
	}
	if err := c.get(ctx, cr, false, c.APIBase+"/albums/"+id, &a); err != nil {
		return nil, friendly(err, "álbum")
	}
	col := &Collection{Kind: "album", ID: id, Name: a.Name, Cover: firstImage(a.Images)}
	if len(a.Artists) > 0 {
		col.Owner = a.Artists[0].Name
	}
	items, next := a.Tracks.Items, a.Tracks.Next
	for {
		for _, r := range items {
			if !r.usable() {
				col.Skipped++
				continue
			}
			t := r.toTrack(a.Name, col.Cover)
			t.Number = len(col.Tracks) + col.Skipped + 1
			col.Tracks = append(col.Tracks, t)
		}
		if next == "" || len(col.Tracks) >= maxTracks {
			break
		}
		var page struct {
			Items []*rawTrack `json:"items"`
			Next  string      `json:"next"`
		}
		if err := c.get(ctx, cr, false, next, &page); err != nil {
			return nil, friendly(err, "álbum")
		}
		items, next = page.Items, page.Next
	}
	if len(col.Tracks) == 0 {
		return nil, errors.New("el álbum no tiene canciones disponibles")
	}
	return col, nil
}

// itemRow cubre las dos formas de la respuesta de playlists: la nueva (campo "item") y la anterior ("track").
type itemRow struct {
	Item  json.RawMessage `json:"item"`
	Track json.RawMessage `json:"track"`
}

func (r itemRow) object() *rawTrack {
	for _, raw := range []json.RawMessage{r.Item, r.Track} {
		if len(raw) == 0 || raw[0] != '{' { // en el formato nuevo, "track" pasó a ser un booleano
			continue
		}
		var t rawTrack
		if json.Unmarshal(raw, &t) == nil {
			return &t
		}
	}
	return nil
}

type itemsPage struct {
	Items []itemRow `json:"items"`
	Next  string    `json:"next"`
}

func (c *Client) fetchPlaylist(ctx context.Context, cr Creds, id string) (*Collection, error) {
	var meta struct {
		Name   string  `json:"name"`
		Images []image `json:"images"`
		Owner  struct {
			DisplayName string `json:"display_name"`
		} `json:"owner"`
	}
	if err := c.get(ctx, cr, true, c.APIBase+"/playlists/"+id+"?fields=name,images,owner(display_name)", &meta); err != nil {
		return nil, friendly(err, "playlist")
	}
	col := &Collection{Kind: "playlist", ID: id, Name: meta.Name, Owner: meta.Owner.DisplayName, Cover: firstImage(meta.Images)}
	if err := c.collectItems(ctx, cr, c.APIBase+"/playlists/"+id+"/items?limit="+strconv.Itoa(pageSize), col, "playlist"); err != nil {
		return nil, err
	}
	return col, nil
}

func (c *Client) fetchLiked(ctx context.Context, cr Creds) (*Collection, error) {
	col := &Collection{Kind: "liked", ID: "me", Name: "Canciones que te gustan"}
	if err := c.collectItems(ctx, cr, c.APIBase+"/me/tracks?limit="+strconv.Itoa(pageSize), col, "biblioteca"); err != nil {
		return nil, err
	}
	return col, nil
}

func (c *Client) collectItems(ctx context.Context, cr Creds, first string, col *Collection, what string) error {
	next := first
	for next != "" && len(col.Tracks) < maxTracks {
		var page itemsPage
		if err := c.get(ctx, cr, true, next, &page); err != nil {
			return friendly(err, what)
		}
		for _, row := range page.Items {
			r := row.object()
			if !r.usable() {
				col.Skipped++
				continue
			}
			t := r.toTrack("", "")
			t.Number = len(col.Tracks) + col.Skipped + 1
			col.Tracks = append(col.Tracks, t)
		}
		next = page.Next
	}
	if len(col.Tracks) == 0 {
		return errors.New("no se encontraron canciones disponibles en esta " + what)
	}
	return nil
}

func detail(ae *apiError) string {
	if ae.Msg == "" {
		return ""
	}
	return " — Spotify dice: «" + ae.Msg + "»"
}

// friendly traduce los errores HTTP más comunes a mensajes accionables.
func friendly(err error, what string) error {
	var ae *apiError
	if !errors.As(err, &ae) {
		return err
	}
	switch ae.Status {
	case http.StatusUnauthorized:
		return errors.New("Spotify rechazó la sesión: vuelve a iniciar sesión en CONFIG")
	case http.StatusForbidden:
		if what == "playlist" || what == "biblioteca" {
			return errors.New("Spotify no permite leer esta " + what + detail(ae) + ": solo se pueden leer las playlists que posees o en las que colaboras (duplícala en tu cuenta). Si ya es tuya, revisa Premium del dueño de la app y que tu cuenta esté en «User Management» de la app")
		}
		return fmt.Errorf("Spotify negó el acceso (403)%s. Causas habituales: el dueño de la app no tiene Premium activo, o tu cuenta no está en «User Management» de la app en developer.spotify.com (modo Development), o el Client ID/Secret son de otra app", detail(ae))
	case http.StatusNotFound:
		if what == "playlist" {
			return errors.New("no se encontró la playlist (¿es privada, ajena o una playlist generada por Spotify?)")
		}
		return errors.New("no se encontró " + what + " en Spotify")
	case http.StatusTooManyRequests:
		return errors.New("Spotify limitó las peticiones; espera un momento e inténtalo de nuevo")
	}
	return err
}

// Test comprueba que las credenciales funcionan y devuelve un mensaje para mostrar.
func (c *Client) Test(ctx context.Context, cr Creds) (string, error) {
	if cr.RefreshToken != "" {
		name, err := c.DisplayName(ctx, cr)
		if err != nil {
			return "", err
		}
		return "Sesión iniciada como " + name, nil
	}
	// Se pide una canción concreta: confirma que el token realmente sirve para la API, no solo que se emitió.
	var probe struct {
		Name string `json:"name"`
	}
	if err := c.get(ctx, cr, false, c.APIBase+"/tracks/11dFghVXANMlKmJXsNCbNl", &probe); err != nil {
		return "", friendly(err, "canción")
	}
	return "Credenciales correctas (sirven para canciones y álbumes; para playlists inicia sesión)", nil
}

// DisplayName devuelve el nombre del usuario con sesión iniciada.
func (c *Client) DisplayName(ctx context.Context, cr Creds) (string, error) {
	var me struct {
		DisplayName string `json:"display_name"`
		ID          string `json:"id"`
	}
	if err := c.get(ctx, cr, true, c.APIBase+"/me", &me); err != nil {
		return "", friendly(err, "cuenta")
	}
	if me.DisplayName != "" {
		return me.DisplayName, nil
	}
	return me.ID, nil
}
