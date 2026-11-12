package web

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"

	"github.com/xaw624/multiparty/internal/espejo"
	"github.com/xaw624/multiparty/internal/pool"
	"github.com/xaw624/multiparty/internal/youtube"
)

const (
	cookieOAuth = "mp_oauth"
	// maxImportar limita cuántas canciones se importan de una vez: cada una
	// consulta varias fuentes públicas.
	maxImportar = 50
)

var (
	rutaGrupo     = regexp.MustCompile(`^/g/[a-z0-9]{4,32}$`)
	idYouTube     = regexp.MustCompile(`^[A-Za-z0-9_-]{2,64}$`)
	errSinYouTube = errors.New("YouTube no está conectado")
)

// ---- conexión OAuth ----

// conectarYouTube manda al usuario a la pantalla de consentimiento de Google.
func (s *Server) conectarYouTube(w http.ResponseWriter, r *http.Request) {
	if s.google == nil {
		http.NotFound(w, r)
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
	valor := state + "|" + verifier + "|" + volver
	http.SetCookie(w, &http.Cookie{
		Name:     cookieOAuth,
		Value:    valor + "~" + s.ses.firma(valor),
		Path:     "/cuentas/youtube/",
		MaxAge:   600,
		HttpOnly: true,
		Secure:   esHTTPS(r),
		SameSite: http.SameSiteLaxMode,
	})
	destino := s.google.AuthCodeURL(state,
		oauth2.AccessTypeOffline, oauth2.ApprovalForce, oauth2.S256ChallengeOption(verifier))
	http.Redirect(w, r, destino, http.StatusFound)
}

// callbackYouTube recibe el código de Google, guarda los tokens cifrados y
// vuelve al grupo.
func (s *Server) callbackYouTube(w http.ResponseWriter, r *http.Request) {
	if s.google == nil {
		http.NotFound(w, r)
		return
	}
	state, verifier, volver, ok := s.leerCookieOAuth(r)
	http.SetCookie(w, &http.Cookie{Name: cookieOAuth, Path: "/cuentas/youtube/", MaxAge: -1})
	if !ok {
		http.Error(w, "La conexión con YouTube caducó; inténtalo de nuevo.", http.StatusBadRequest)
		return
	}
	irA := func(aviso string) {
		http.Redirect(w, r, volver+"?aviso="+aviso, http.StatusSeeOther)
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
	tok, err := s.google.Exchange(ctx, q.Get("code"), oauth2.VerifierOption(verifier))
	if err != nil {
		s.log.Warn("intercambio OAuth fallido", "err", err)
		irA("error")
		return
	}
	if alcance, _ := tok.Extra("scope").(string); alcance != "" && !strings.Contains(alcance, youtube.Scope) {
		irA("sin-permiso") // el usuario desmarcó el permiso de YouTube
		return
	}
	canalID, canal, err := s.clienteConToken(ctx, tok).CanalPropio(ctx)
	if errors.Is(err, youtube.ErrSinCanal) {
		irA("sin-canal")
		return
	}
	if err != nil {
		s.log.Warn("leer canal de YouTube", "err", err)
		irA("error")
		return
	}
	c := pool.Cuenta{
		UsuarioID:     uid,
		Plataforma:    "youtube",
		IDExterno:     canalID,
		NombreExterno: canal,
		AccessCifrado: s.caja.Sellar([]byte(tok.AccessToken)),
		Expira:        tok.Expiry,
		Scopes:        youtube.Scope,
	}
	if tok.RefreshToken != "" {
		c.RefreshCifrado = s.caja.Sellar([]byte(tok.RefreshToken))
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
	irA("youtube-conectado")
}

func (s *Server) leerCookieOAuth(r *http.Request) (state, verifier, volver string, ok bool) {
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
	partes := strings.SplitN(valor, "|", 3)
	if len(partes) != 3 || !(partes[2] == "/" || rutaGrupo.MatchString(partes[2])) {
		return
	}
	return partes[0], partes[1], partes[2], true
}

// desconectarYouTube revoca el permiso en Google y borra los tokens.
func (s *Server) desconectarYouTube(w http.ResponseWriter, r *http.Request) {
	if s.google == nil {
		http.NotFound(w, r)
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
	if c, err := s.store.CuentaDe(r.Context(), uid, "youtube"); err == nil {
		if refresh, err := s.caja.Abrir(c.RefreshCifrado); err == nil {
			s.revocar(r.Context(), string(refresh))
		}
	}
	if err := s.store.BorrarCuenta(r.Context(), uid, "youtube"); err != nil {
		s.fallo(w, err)
		return
	}
	http.Redirect(w, r, volver+"?aviso=youtube-desconectado", http.StatusSeeOther)
}

// revocar invalida el token en Google. Si falla, el usuario aún puede
// quitar el acceso en myaccount.google.com; los tokens locales se borran igual.
func (s *Server) revocar(ctx context.Context, token string) {
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

// ---- cliente autenticado ----

func (s *Server) clienteConToken(ctx context.Context, tok *oauth2.Token) *youtube.Client {
	return s.clienteConFuente(ctx, s.google.TokenSource(ctx, tok))
}

func (s *Server) clienteConFuente(ctx context.Context, ts oauth2.TokenSource) *youtube.Client {
	hc := oauth2.NewClient(ctx, ts)
	hc.Timeout = 20 * time.Second
	if s.youtubeBase != "" {
		return youtube.NewConBase(hc, s.youtubeBase)
	}
	return youtube.New(hc)
}

// clienteYouTube arma un cliente con los tokens guardados del usuario. La
// función devuelta guarda el access token si se renovó; hay que llamarla al
// terminar.
func (s *Server) clienteYouTube(ctx context.Context, uid int64) (*youtube.Client, func(), error) {
	if s.google == nil {
		return nil, nil, errSinYouTube
	}
	c, err := s.store.CuentaDe(ctx, uid, "youtube")
	if errors.Is(err, pool.ErrNoEncontrado) || (err == nil && len(c.RefreshCifrado) == 0) {
		return nil, nil, errSinYouTube
	}
	if err != nil {
		return nil, nil, err
	}
	refresh, err := s.caja.Abrir(c.RefreshCifrado)
	if err != nil {
		return nil, nil, fmt.Errorf("descifrar refresh token: %w", err)
	}
	tok := &oauth2.Token{RefreshToken: string(refresh), Expiry: c.Expira, TokenType: "Bearer"}
	if access, err := s.caja.Abrir(c.AccessCifrado); err == nil {
		tok.AccessToken = string(access)
	}
	ts := s.google.TokenSource(context.WithoutCancel(ctx), tok)
	guardar := func() {
		nuevo, err := ts.Token()
		if err != nil || nuevo.AccessToken == tok.AccessToken {
			return
		}
		if err := s.store.ActualizarTokens(context.WithoutCancel(ctx), uid, "youtube",
			s.caja.Sellar([]byte(nuevo.AccessToken)), nuevo.Expiry); err != nil {
			s.log.Warn("guardar token renovado", "err", err)
		}
	}
	return s.clienteConFuente(ctx, ts), guardar, nil
}

// ---- playlist espejo ----

func (s *Server) crearEspejoYouTube(w http.ResponseWriter, r *http.Request) {
	g, uid, ok := s.requerirMiembro(w, r)
	if !ok {
		return
	}
	yt, guardar, err := s.clienteYouTube(r.Context(), uid)
	if err != nil {
		s.responder(w, r, g, uid, aviso{Error: s.mensajeYouTube(r.Context(), uid, err)})
		return
	}
	defer guardar()
	id, err := yt.CrearPlaylist(r.Context(), "Multiparty · "+g.Nombre,
		"Tu ventana de 10 canciones del grupo «"+g.Nombre+"» en Multiparty. "+
			"La app agrega y quita canciones solo cuando tú lo pides.")
	if err == nil {
		err = s.store.CrearEspejo(r.Context(), g.ID, uid, "youtube", id)
	}
	if err != nil {
		s.responder(w, r, g, uid, aviso{Error: s.mensajeYouTube(r.Context(), uid, err)})
		return
	}
	s.responder(w, r, g, uid, s.sincronizar(r.Context(), g, uid, true))
}

func (s *Server) sincronizarHandler(w http.ResponseWriter, r *http.Request) {
	g, uid, ok := s.requerirMiembro(w, r)
	if !ok {
		return
	}
	s.responder(w, r, g, uid, s.sincronizar(r.Context(), g, uid, true))
}

// sincronizar actualiza la playlist del usuario. Con resumen=false solo
// avisa si algo salió mal (para no llenar de mensajes "Ya la escuché").
func (s *Server) sincronizar(ctx context.Context, g pool.Grupo, uid int64, resumen bool) aviso {
	yt, guardar, err := s.clienteYouTube(ctx, uid)
	if err != nil {
		return aviso{Error: s.mensajeYouTube(ctx, uid, err)}
	}
	defer guardar()
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	res, err := espejo.Sincronizar(ctx, s.store, yt, g.ID, uid)
	if err != nil {
		return aviso{Error: s.mensajeYouTube(ctx, uid, err)}
	}
	if !resumen && res.Omitidas == 0 {
		return aviso{}
	}
	return aviso{Mensaje: resumenSincronizacion(res)}
}

func resumenSincronizacion(r espejo.Resultado) string {
	var partes []string
	if r.Agregadas > 0 {
		partes = append(partes, plural(r.Agregadas, "agregada", "agregadas"))
	}
	if r.Quitadas > 0 {
		partes = append(partes, plural(r.Quitadas, "quitada", "quitadas"))
	}
	msg := "Tu playlist está al día."
	if len(partes) > 0 {
		msg = "Playlist actualizada: " + strings.Join(partes, ", ") + "."
	}
	if r.Omitidas > 0 {
		msg += " " + plural(r.Omitidas, "canción no está", "canciones no están") +
			" en YouTube; ábrelas con su link."
	}
	return msg
}

func plural(n int, uno, varios string) string {
	if n == 1 {
		return "1 " + uno
	}
	return strconv.Itoa(n) + " " + varios
}

// mensajeYouTube traduce errores de YouTube y, si el token ya no sirve,
// marca la cuenta para que el usuario vuelva a conectar.
func (s *Server) mensajeYouTube(ctx context.Context, uid int64, err error) string {
	switch {
	case errors.Is(err, errSinYouTube):
		return "Primero conecta tu cuenta de YouTube."
	case errors.Is(err, youtube.ErrCuota):
		return "Se agotó la cuota diaria de YouTube. Lo que falte se completará mañana con «Sincronizar»."
	case errors.Is(err, youtube.ErrReconectar):
		if e := s.store.InvalidarCuenta(context.WithoutCancel(ctx), uid, "youtube"); e != nil {
			s.log.Warn("invalidar cuenta", "err", e)
		}
		return "Tu conexión con YouTube caducó. Vuelve a conectar YouTube."
	case errors.Is(err, youtube.ErrSinCanal):
		return "Tu cuenta de Google no tiene canal de YouTube. Crea uno en youtube.com y vuelve a intentar."
	case errors.Is(err, youtube.ErrNoEncontrado):
		return "No encontré tu playlist en YouTube (¿la borraste?). Crea una nueva."
	case errors.Is(err, pool.ErrNoEncontrado):
		return "Primero crea tu playlist en YouTube Music."
	default:
		s.log.Error("acción de YouTube fallida", "err", err)
		return "No se pudo hablar con YouTube. Intenta de nuevo."
	}
}

// avisoDeURL convierte los parámetros de una redirección en un mensaje. Solo
// usa textos fijos y números: nunca refleja texto de la URL.
func avisoDeURL(q url.Values) aviso {
	switch q.Get("aviso") {
	case "youtube-conectado":
		return aviso{Mensaje: "YouTube conectado."}
	case "youtube-desconectado":
		return aviso{Mensaje: "YouTube desconectado. Tu playlist sigue en tu cuenta, pero ya no se actualiza."}
	case "cancelado":
		return aviso{Error: "Cancelaste la conexión con YouTube."}
	case "sin-canal":
		return aviso{Error: "Tu cuenta de Google no tiene canal de YouTube. Crea uno en youtube.com y vuelve a intentar."}
	case "sin-permiso":
		return aviso{Error: "Para crear tu playlist, Multiparty necesita permiso para administrar tu cuenta de YouTube."}
	case "en-uso":
		return aviso{Error: "Esa cuenta de YouTube ya está conectada por otra persona."}
	case "error":
		return aviso{Error: "No se pudo conectar con YouTube. Intenta de nuevo."}
	}
	if q.Has("importadas") {
		n := func(k string) int { v, _ := strconv.Atoi(q.Get(k)); return max(v, 0) }
		msg := "Importación: " + plural(n("importadas"), "canción agregada", "canciones agregadas")
		if r := n("repetidas"); r > 0 {
			msg += ", " + plural(r, "ya estaba", "ya estaban")
		}
		if f := n("fallidas"); f > 0 {
			msg += ", " + plural(f, "no se encontró", "no se encontraron")
		}
		return aviso{Mensaje: msg + "."}
	}
	return aviso{}
}

// ---- importar desde playlists de YouTube ----

type datosImportar struct {
	Grupo          pool.Grupo
	Playlists      []youtube.Playlist
	Videos         []youtube.Video
	PlaylistTitulo string
	Max            int
	Error          string
}

func (s *Server) importarVer(w http.ResponseWriter, r *http.Request) {
	g, uid, ok := s.requerirMiembro(w, r)
	if !ok {
		return
	}
	d := datosImportar{Grupo: g, Max: maxImportar}
	yt, guardar, err := s.clienteYouTube(r.Context(), uid)
	if err != nil {
		d.Error = s.mensajeYouTube(r.Context(), uid, err)
		s.render(w, "importar.html", d)
		return
	}
	defer guardar()

	if id := r.URL.Query().Get("playlist"); id != "" {
		if !idYouTube.MatchString(id) {
			http.Error(w, "Playlist inválida.", http.StatusBadRequest)
			return
		}
		d.PlaylistTitulo = r.URL.Query().Get("t")
		d.Videos, err = yt.ItemsDePlaylist(r.Context(), id, 200)
	} else {
		d.Playlists, err = yt.MisPlaylists(r.Context())
	}
	if err != nil {
		d.Error = s.mensajeYouTube(r.Context(), uid, err)
	}
	s.render(w, "importar.html", d)
}

func (s *Server) importar(w http.ResponseWriter, r *http.Request) {
	g, uid, ok := s.requerirMiembro(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Formulario inválido.", http.StatusBadRequest)
		return
	}
	var ids []string
	for _, id := range r.PostForm["v"] {
		if idYouTube.MatchString(id) && len(ids) < maxImportar {
			ids = append(ids, id)
		}
	}

	// Resolver en paralelo (4 a la vez) y agregar en el orden de la playlist.
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	resueltas := make([]pool.CancionResuelta, len(ids))
	errs := make([]error, len(ids))
	var wg sync.WaitGroup
	turno := make(chan struct{}, 4)
	for i, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			turno <- struct{}{}
			defer func() { <-turno }()
			resueltas[i], errs[i] = s.resolver.Resolver(ctx, "https://music.youtube.com/watch?v="+id)
		}()
	}
	wg.Wait()

	var importadas, repetidas, fallidas int
	for i := range ids {
		err := errs[i]
		if err == nil {
			err = s.store.AgregarCancion(r.Context(), g.ID, uid, resueltas[i], pool.Importada)
		}
		var dup pool.ErrDuplicada
		switch {
		case err == nil:
			importadas++
		case errors.As(err, &dup):
			repetidas++
		default:
			fallidas++
			s.log.Info("no se pudo importar", "video", ids[i], "err", err)
		}
	}
	http.Redirect(w, r, fmt.Sprintf("/g/%s?importadas=%d&repetidas=%d&fallidas=%d",
		g.Codigo, importadas, repetidas, fallidas), http.StatusSeeOther)
}
