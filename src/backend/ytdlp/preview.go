package ytdlp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Entry es una canción dentro de una vista previa.
type Entry struct {
	ID        string  `json:"id"`
	Title     string  `json:"title"`
	Uploader  string  `json:"uploader"`
	URL       string  `json:"url"`
	Thumbnail string  `json:"thumbnail"`
	Duration  float64 `json:"duration"` // segundos; 0 si se desconoce
	Index     int     `json:"index"`    // posición 1-based dentro de la playlist (0 en tracks sueltos)
}

// errEmpty se devuelve cuando una playlist o una búsqueda no trae nada utilizable.
var errEmpty = errors.New("la playlist está vacía o no tiene canciones accesibles")

// Preview describe lo que hay detrás de una URL: un track suelto o una playlist.
type Preview struct {
	Source     string  `json:"source"` // "web" (yt-dlp) o "spotify"
	SourceURL  string  `json:"sourceUrl"`
	Title      string  `json:"title"`
	Uploader   string  `json:"uploader"`
	IsPlaylist bool    `json:"isPlaylist"`
	Entries    []Entry `json:"entries"`
	// Warning se rellena cuando yt-dlp falló y solo se pudo mostrar información básica.
	Warning string `json:"warning,omitempty"`
}

type rawThumb struct {
	URL string `json:"url"`
}

type rawInfo struct {
	Type       string     `json:"_type"`
	ID         string     `json:"id"`
	Title      string     `json:"title"`
	Uploader   string     `json:"uploader"`
	Channel    string     `json:"channel"`
	WebpageURL string     `json:"webpage_url"`
	URL        string     `json:"url"`
	Duration   float64    `json:"duration"`
	Thumbnail  string     `json:"thumbnail"`
	Thumbnails []rawThumb `json:"thumbnails"`
	Entries    []*rawInfo `json:"entries"`
}

func (r *rawInfo) uploader() string {
	if r.Uploader != "" {
		return r.Uploader
	}
	return r.Channel
}

func (r *rawInfo) thumb() string {
	if r.Thumbnail != "" {
		return r.Thumbnail
	}
	if n := len(r.Thumbnails); n > 0 {
		return r.Thumbnails[n-1].URL
	}
	return ""
}

func (r *rawInfo) entryURL() string {
	if r.WebpageURL != "" {
		return r.WebpageURL
	}
	return r.URL
}

// parsePreview convierte el JSON de `yt-dlp --flat-playlist --dump-single-json` en un Preview.
func parsePreview(data []byte, requestedURL string) (*Preview, error) {
	var raw rawInfo
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("respuesta de yt-dlp ilegible: %w", err)
	}
	p := &Preview{SourceURL: requestedURL, Title: raw.Title, Uploader: raw.uploader()}

	if raw.Type == "playlist" || len(raw.Entries) > 0 {
		p.IsPlaylist = true
		if p.Title == "" {
			p.Title = "Playlist"
		}
		for i, e := range raw.Entries {
			if e == nil || e.entryURL() == "" {
				continue // entradas privadas o borradas llegan como null
			}
			title := e.Title
			if title == "" {
				title = e.entryURL()
			}
			p.Entries = append(p.Entries, Entry{
				ID:        e.ID,
				Title:     title,
				Uploader:  e.uploader(),
				URL:       e.entryURL(),
				Thumbnail: e.thumb(),
				Duration:  e.Duration,
				Index:     i + 1, // se conserva la posición original aunque se salten entradas
			})
		}
		if len(p.Entries) == 0 {
			return nil, errEmpty
		}
		return p, nil
	}

	u := raw.entryURL()
	if u == "" {
		u = requestedURL
	}
	if p.Title == "" {
		p.Title = u
	}
	p.Entries = []Entry{{
		ID:        raw.ID,
		Title:     p.Title,
		Uploader:  raw.uploader(),
		URL:       u,
		Thumbnail: raw.thumb(),
		Duration:  raw.Duration,
	}}
	return p, nil
}

// Preview consulta a yt-dlp qué contiene la URL sin descargar nada.
func (c *Client) Preview(ctx context.Context, rawURL string) (*Preview, error) {
	rawURL = strings.TrimSpace(rawURL)
	if err := validateURL(rawURL); err != nil {
		return nil, err
	}
	p, err := c.extract(ctx, rawURL, rawURL)
	if err != nil {
		return nil, err
	}
	p.Source = "web"
	return p, nil
}

// Search busca en YouTube y devuelve hasta n resultados (con título, canal y duración) sin descargar nada.
// Una búsqueda sin resultados devuelve una lista vacía, no un error.
func (c *Client) Search(ctx context.Context, query string, n int) ([]Entry, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("búsqueda vacía")
	}
	n = max(1, min(n, 15))
	p, err := c.extract(ctx, fmt.Sprintf("ytsearch%d:%s", n, query), "")
	if errors.Is(err, errEmpty) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return p.Entries, nil
}

// extract ejecuta `yt-dlp --flat-playlist --dump-single-json` sobre un destino ya validado.
// Si yt-dlp falla no se disimula: o se devuelve su error real o, para un video de YouTube,
// una vista previa básica (oEmbed) con un aviso visible con el motivo.
func (c *Client) extract(ctx context.Context, rawURL, requested string) (*Preview, error) {
	path, _, err := c.Resolve()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	args := []string{"--ignore-config", "--flat-playlist", "--dump-single-json", "--no-warnings", "--ignore-errors"}
	args = append(args, c.commonArgs(ctx, path)...)
	cmd := c.command(ctx, path, append(args, "--", rawURL)...)
	var stderr tailBuffer
	cmd.Stderr = &stderr
	out, runErr := cmd.Output()

	var parsed *Preview
	var parseErr error
	if len(out) > 0 {
		parsed, parseErr = parsePreview(out, requested)
	}
	// Salida limpia y utilizable.
	if runErr == nil && parsed != nil {
		return parsed, nil
	}
	if runErr == nil && parseErr != nil {
		return nil, parseErr
	}
	if ctx.Err() != nil {
		return nil, fmt.Errorf("tiempo agotado o cancelado al analizar la URL")
	}
	reason := errorMessage(stderr.String())
	// yt-dlp salió con error: un resultado parcial con entradas reales se conserva, pero avisando.
	if parsed != nil && (parsed.IsPlaylist || parsed.hasRealTitle()) {
		parsed.Warning = reason
		return parsed, nil
	}
	if requested != "" {
		if p := oembedPreview(ctx, requested); p != nil {
			p.Warning = reason
			return p, nil
		}
	}
	return nil, errors.New(reason)
}

func (p *Preview) hasRealTitle() bool {
	return len(p.Entries) > 0 && p.Title != "" && p.Title != p.Entries[0].URL
}

// OEmbedBase es el endpoint público de oEmbed de YouTube (variable para poder cambiarla en tests).
var OEmbedBase = "https://www.youtube.com/oembed"

var ytIDRe = regexp.MustCompile(`(?:youtu\.be/|[?&]v=|/shorts/|/embed/)([A-Za-z0-9_-]{11})`)

// YouTubeID extrae el id de 11 caracteres de una URL de video de YouTube ("" si no lo es).
func YouTubeID(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	h := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	if h != "youtube.com" && h != "m.youtube.com" && h != "music.youtube.com" && h != "youtu.be" {
		return ""
	}
	if m := ytIDRe.FindStringSubmatch(raw); m != nil {
		return m[1]
	}
	return ""
}

// oembedPreview pide título y canal al endpoint público de YouTube, que no exige verificación de bot.
// Devuelve nil si la URL no es un video de YouTube o la consulta falla.
func oembedPreview(ctx context.Context, rawURL string) *Preview {
	id := YouTubeID(rawURL)
	if id == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	q := OEmbedBase + "?format=json&url=" + url.QueryEscape("https://www.youtube.com/watch?v="+id)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, q, nil)
	if err != nil {
		return nil
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var v struct {
		Title  string `json:"title"`
		Author string `json:"author_name"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&v) != nil || v.Title == "" {
		return nil
	}
	e := Entry{ID: id, Title: v.Title, Uploader: v.Author, URL: rawURL, Thumbnail: "https://i.ytimg.com/vi/" + id + "/hqdefault.jpg"}
	return &Preview{SourceURL: rawURL, Title: v.Title, Uploader: v.Author, Entries: []Entry{e}}
}
