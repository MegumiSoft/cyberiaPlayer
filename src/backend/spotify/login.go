package spotify

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// RedirectPort es el puerto fijo del callback. Spotify exige registrar la URI exacta en el panel
// de desarrollador, por eso no puede ser aleatorio. Se usa la IP literal porque Spotify ya no
// admite "localhost" en las URIs de redirección.
const RedirectPort = 8888

func RedirectURI() string { return fmt.Sprintf("http://127.0.0.1:%d/callback", RedirectPort) }

const Scopes = "playlist-read-private playlist-read-collaborative user-library-read"

func randomString(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // sin entropía no hay forma segura de continuar
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// Challenge calcula el code_challenge S256 de PKCE (RFC 7636).
func Challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// AuthorizeURL arma la dirección a la que hay que enviar al usuario.
func (c *Client) AuthorizeURL(clientID, redirect, state, challenge string) string {
	q := url.Values{
		"client_id":             {clientID},
		"response_type":         {"code"},
		"redirect_uri":          {redirect},
		"scope":                 {Scopes},
		"state":                 {state},
		"code_challenge_method": {"S256"},
		"code_challenge":        {challenge},
	}
	return c.AuthBase + "/authorize?" + q.Encode()
}

const loginPage = `<!doctype html><meta charset="utf-8"><title>Cyberia</title>
<body style="background:#0e0e10;color:#e8e8ee;font:18px/1.5 system-ui;display:grid;place-items:center;height:100vh;margin:0">
<div style="text-align:center"><div style="font-size:42px;background:linear-gradient(90deg,#18e0ff,#ff2fb0,#ffe600);-webkit-background-clip:text;color:transparent">CYBERIA</div>
<p>%s</p><p style="color:#8a8a99">Ya puedes cerrar esta pestaña.</p></div>`

// Login ejecuta el flujo de autorización con PKCE y devuelve el refresh token.
// open recibe la URL que hay que abrir en el navegador. Espera como máximo timeout.
func (c *Client) Login(ctx context.Context, clientID string, port int, timeout time.Duration, open func(string)) (string, error) {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return "", ErrNoCredentials
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return "", fmt.Errorf("no se pudo abrir el puerto %d para el login (¿otra copia de la app o un programa lo usa?): %w", port, err)
	}
	redirect := fmt.Sprintf("http://127.0.0.1:%d/callback", ln.Addr().(*net.TCPAddr).Port)
	verifier := randomString(64)
	state := randomString(16)

	type result struct {
		code string
		err  error
	}
	done := make(chan result, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		var res result
		switch {
		case q.Get("state") != state:
			res.err = errors.New("respuesta de login inválida (state distinto)")
		case q.Get("error") != "":
			res.err = fmt.Errorf("Spotify canceló el login: %s", q.Get("error"))
		case q.Get("code") == "":
			res.err = errors.New("Spotify no devolvió código de autorización")
		default:
			res.code = q.Get("code")
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if res.err != nil {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, loginPage, html.EscapeString(res.err.Error()))
		} else {
			fmt.Fprintf(w, loginPage, "Sesión iniciada correctamente.")
		}
		select {
		case done <- res:
		default:
		}
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	defer func() {
		sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()

	open(c.AuthorizeURL(clientID, redirect, state, Challenge(verifier)))

	var res result
	select {
	case res = <-done:
	case <-time.After(timeout):
		return "", errors.New("se agotó el tiempo esperando el login de Spotify")
	case <-ctx.Done():
		return "", ctx.Err()
	}
	if res.err != nil {
		return "", res.err
	}
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {res.code},
		"redirect_uri":  {redirect},
		"client_id":     {clientID},
		"code_verifier": {verifier},
	}
	_, refresh, err := c.postToken(ctx, form, "", "")
	if err != nil {
		var ae *apiError
		if errors.As(err, &ae) && ae.Status == http.StatusBadRequest {
			return "", fmt.Errorf("Spotify rechazó el intercambio del código. Revisa que la URI de redirección %s esté registrada en tu app (%w)", RedirectURI(), err)
		}
		return "", err
	}
	if refresh == "" {
		return "", errors.New("Spotify no devolvió una sesión renovable")
	}
	return refresh, nil
}
