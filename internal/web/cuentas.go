package web

import (
	"context"
	"crypto/rand"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"github.com/xaw624/multiparty/internal/conexion"
	"github.com/xaw624/multiparty/internal/pool"
	"github.com/xaw624/multiparty/internal/spotify"
	"github.com/xaw624/multiparty/internal/youtube"
)

const cookieOAuth = "mp_oauth"

var rutaGrupo = regexp.MustCompile(`^/g/[a-z0-9]{4,32}$`)

// scopesRequeridos son los permisos sin los que la conexión no sirve.
var scopesRequeridos = map[string][]string{
	conexion.YouTube: {youtube.Scope},
	conexion.Spotify: spotify.Scopes,
}

// plataformaOAuth lee {plataforma} de la ruta; responde 404 si no está habilitada.
func (s *Server) plataformaOAuth(w http.ResponseWriter, r *http.Request) (string, bool) {
	p := r.PathValue("plataforma")
	if !s.con.Habilitada(p) {
		http.NotFound(w, r)
		return "", false
	}
	return p, true
}

// conectar manda al usuario a la pantalla de consentimiento de la plataforma.
func (s *Server) conectar(w http.ResponseWriter, r *http.Request) {
	p, ok := s.plataformaOAuth(w, r)
	if !ok {
		return
	}
	if _, _, ok := s.usuarioActual(r); !ok {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	volver := r.URL.Query().Get("volver")
	if !rutaGrupo.MatchString(volver) {
		volver = "/" // evita redirecciones abiertas
	}
	state := rand.Text()
	verifier := oauth2.GenerateVerifier()
	valor := strings.Join([]string{state, verifier, volver, p}, "|")
	http.SetCookie(w, &http.Cookie{
		Name:     cookieOAuth,
		Value:    valor + "~" + s.ses.firma(valor),
		Path:     "/cuentas/",
		MaxAge:   600,
		HttpOnly: true,
		Secure:   esHTTPS(r),
		SameSite: http.SameSiteLaxMode,
	})
	opciones := []oauth2.AuthCodeOption{oauth2.S256ChallengeOption(verifier)}
	switch p {
	case conexion.YouTube:
		opciones = append(opciones, oauth2.AccessTypeOffline, oauth2.ApprovalForce)
	case conexion.Spotify:
		// Deja elegir otra cuenta si el navegador ya tiene una sesión de Spotify.
		opciones = append(opciones, oauth2.SetAuthURLParam("show_dialog", "true"))
	}
	http.Redirect(w, r, s.con.OAuth(p).AuthCodeURL(state, opciones...), http.StatusFound)
}

// callback recibe el código, guarda los tokens cifrados y vuelve al grupo.
func (s *Server) callback(w http.ResponseWriter, r *http.Request) {
	p, ok := s.plataformaOAuth(w, r)
	if !ok {
		return
	}
	state, verifier, volver, deCookie, ok := s.leerCookieOAuth(r)
	http.SetCookie(w, &http.Cookie{Name: cookieOAuth, Path: "/cuentas/", MaxAge: -1})
	if !ok || deCookie != p {
		http.Error(w, "La conexión caducó; inténtalo de nuevo.", http.StatusBadRequest)
		return
	}
	irA := func(aviso string) {
		http.Redirect(w, r, volver+"?aviso="+aviso+"&p="+p, http.StatusSeeOther)
	}
	q := r.URL.Query()
	if q.Get("error") != "" {
		irA("cancelado")
		return
	}
	if q.Get("state") != state {
		http.Error(w, "Estado OAuth inválido.", http.StatusBadRequest)
		return
	}
	uid, _, conSesion := s.usuarioActual(r)
	if !conSesion {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	tok, err := s.con.OAuth(p).Exchange(ctx, q.Get("code"), oauth2.VerifierOption(verifier))
	if err != nil {
		s.log.Warn("intercambio OAuth fallido", "plataforma", p, "err", err)
		irA("error")
		return
	}
	if alcance, _ := tok.Extra("scope").(string); alcance != "" {
		for _, req := range scopesRequeridos[p] {
			if !strings.Contains(alcance, req) {
				irA("sin-permiso") // el usuario desmarcó un permiso
				return
			}
		}
	}

	var idExterno, nombre string
	switch p {
	case conexion.YouTube:
		idExterno, nombre, err = s.con.YouTubeConToken(ctx, tok).CanalPropio(ctx)
	case conexion.Spotify:
		idExterno, nombre, err = s.con.SpotifyConToken(ctx, tok).Yo(ctx)
	}
	switch {
	case errors.Is(err, youtube.ErrSinCanal):
		irA("sin-canal")
		return
	case errors.Is(err, spotify.ErrNoAutorizado):
		irA("no-autorizado")
		return
	case err != nil:
		s.log.Warn("leer cuenta", "plataforma", p, "err", err)
		irA("error")
		return
	}

	caja := s.con.Caja()
	c := pool.Cuenta{
		UsuarioID:     uid,
		Plataforma:    p,
		IDExterno:     idExterno,
		NombreExterno: nombre,
		AccessCifrado: caja.Sellar([]byte(tok.AccessToken)),
		Expira:        tok.Expiry,
		Scopes:        strings.Join(scopesRequeridos[p], " "),
	}
	if tok.RefreshToken != "" {
		c.RefreshCifrado = caja.Sellar([]byte(tok.RefreshToken))
	}
	err = s.store.GuardarCuenta(ctx, c)
	if errors.Is(err, pool.ErrCuentaEnUso) {
		irA("en-uso")
		return
	}
	if err != nil {
		s.fallo(w, err)
		return
	}
	irA("conectado")
}

func (s *Server) leerCookieOAuth(r *http.Request) (state, verifier, volver, plataforma string, ok bool) {
	c, err := r.Cookie(cookieOAuth)
	if err != nil {
		return
	}
	i := strings.LastIndex(c.Value, "~")
	if i < 0 {
		return
	}
	valor, firma := c.Value[:i], c.Value[i+1:]
	if firma != s.ses.firma(valor) {
		return
	}
	partes := strings.Split(valor, "|")
	if len(partes) != 4 || !(partes[2] == "/" || rutaGrupo.MatchString(partes[2])) {
		return
	}
	return partes[0], partes[1], partes[2], partes[3], true
}

// desconectar borra los tokens. En Google además revoca el permiso; Spotify
// no tiene API para revocar (se hace en spotify.com/account/apps).
func (s *Server) desconectar(w http.ResponseWriter, r *http.Request) {
	p, ok := s.plataformaOAuth(w, r)
	if !ok {
		return
	}
	uid, _, ok := s.usuarioActual(r)
	volver := r.FormValue("volver")
	if !rutaGrupo.MatchString(volver) {
		volver = "/"
	}
	if !ok {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if p == conexion.YouTube {
		if c, err := s.store.CuentaDe(r.Context(), uid, p); err == nil {
			if refresh, err := s.con.Caja().Abrir(c.RefreshCifrado); err == nil {
				s.revocarGoogle(r.Context(), string(refresh))
			}
		}
	}
	if err := s.store.BorrarCuenta(r.Context(), uid, p); err != nil {
		s.fallo(w, err)
		return
	}
	http.Redirect(w, r, volver+"?aviso=desconectado&p="+p, http.StatusSeeOther)
}

// revocarGoogle invalida el token en Google. Si falla, los tokens locales
// se borran igual y el usuario puede quitar el acceso en su cuenta de Google.
func (s *Server) revocarGoogle(ctx context.Context, token string) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, s.revocarURL,
		strings.NewReader(url.Values{"token": {token}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.log.Warn("revocar token de Google", "err", err)
		return
	}
	resp.Body.Close()
}
