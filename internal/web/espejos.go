package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/xaw624/multiparty/internal/conexion"
	"github.com/xaw624/multiparty/internal/espejo"
	"github.com/xaw624/multiparty/internal/pool"
	"github.com/xaw624/multiparty/internal/spotify"
	"github.com/xaw624/multiparty/internal/youtube"
)

// maxImportar limita cuántas canciones se importan de una vez.
const maxImportar = 50

var idPlataforma = regexp.MustCompile(`^[A-Za-z0-9_-]{2,64}$`)

// ---- playlist espejo ----

func (s *Server) crearEspejo(w http.ResponseWriter, r *http.Request) {
	g, uid, ok := s.requerirMiembro(w, r)
	if !ok {
		return
	}
	p := r.PathValue("plataforma")
	if !s.con.Habilitada(p) {
		http.NotFound(w, r)
		return
	}
	nombre := "Multiparty · " + g.Nombre
	desc := "Tu ventana de 10 canciones del grupo «" + g.Nombre + "» en Multiparty."
	var id string
	var err error
	switch p {
	case conexion.YouTube:
		yt, guardar, errC := s.con.YouTube(r.Context(), uid)
		if err = errC; err == nil {
			defer guardar()
			id, err = yt.CrearPlaylist(r.Context(), nombre, desc+" La app agrega y quita canciones solo cuando tú lo pides.")
		}
	case conexion.Spotify:
		sp, guardar, errC := s.con.Spotify(r.Context(), uid)
		if err = errC; err == nil {
			defer guardar()
			id, err = sp.CrearPlaylist(r.Context(), nombre, desc+" Se actualiza sola cuando escuchas.")
		}
	}
	if err == nil {
		err = s.store.CrearEspejo(r.Context(), g.ID, uid, p, id)
	}
	if err != nil {
		s.responder(w, r, g, uid, aviso{Error: s.mensajePlataforma(r.Context(), uid, p, err)})
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

// sincronizar actualiza la playlist espejo del usuario. Con resumen=false
// solo avisa si algo salió mal (para no llenar de mensajes "Ya la escuché").
func (s *Server) sincronizar(ctx context.Context, g pool.Grupo, uid int64, resumen bool) aviso {
	e, err := s.store.EspejoDe(ctx, g.ID, uid)
	if err != nil {
		return aviso{Error: s.mensajePlataforma(ctx, uid, "", err)}
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	var res espejo.Resultado
	switch e.Plataforma {
	case conexion.YouTube:
		yt, guardar, errC := s.con.YouTube(ctx, uid)
		if err = errC; err == nil {
			defer guardar()
			res, err = espejo.SincronizarYouTube(ctx, s.store, yt, g.ID, uid)
		}
	case conexion.Spotify:
		sp, guardar, errC := s.con.Spotify(ctx, uid)
		if err = errC; err == nil {
			defer guardar()
			res, err = espejo.SincronizarSpotify(ctx, s.store, sp, g.ID, uid)
		}
	default:
		err = conexion.ErrNoConectado
	}
	if err != nil {
		return aviso{Error: s.mensajePlataforma(ctx, uid, e.Plataforma, err)}
	}
	if !resumen && res.Omitidas == 0 {
		return aviso{}
	}
	return aviso{Mensaje: resumenSincronizacion(res, conexion.Nombre[e.Plataforma])}
}

func resumenSincronizacion(r espejo.Resultado, plataforma string) string {
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
			" en " + plataforma + "; ábrelas con su link o revísalas."
	}
	return msg
}

func plural(n int, uno, varios string) string {
	if n == 1 {
		return "1 " + uno
	}
	return strconv.Itoa(n) + " " + varios
}

// mensajePlataforma traduce errores de YouTube y Spotify y, si el token ya no
// sirve, marca la cuenta para que el usuario vuelva a conectar.
func (s *Server) mensajePlataforma(ctx context.Context, uid int64, p string, err error) string {
	nombre := conexion.Nombre[p]
	if nombre == "" {
		nombre = "la plataforma"
	}
	switch {
	case errors.Is(err, conexion.ErrNoConectado):
		return "Primero conecta tu cuenta de " + nombre + "."
	case errors.Is(err, youtube.ErrReconectar), errors.Is(err, spotify.ErrReconectar):
		if e := s.store.InvalidarCuenta(context.WithoutCancel(ctx), uid, p); e != nil {
			s.log.Warn("invalidar cuenta", "err", e)
		}
		return "Tu conexión con " + nombre + " caducó. Vuelve a conectar " + nombre + "."
	case errors.Is(err, youtube.ErrCuota):
		return "Se agotó la cuota diaria de YouTube. Lo que falte se completará mañana con «Sincronizar»."
	case errors.Is(err, youtube.ErrSinCanal):
		return "Tu cuenta de Google no tiene canal de YouTube. Crea uno en youtube.com y vuelve a intentar."
	case errors.Is(err, spotify.ErrNoAutorizado):
		return "Tu cuenta de Spotify no está autorizada en la app. Pide que la agreguen en el panel de Spotify for Developers."
	case errors.Is(err, spotify.ErrLimite):
		return "Spotify pidió esperar un momento. Intenta de nuevo en un minuto."
	case errors.Is(err, youtube.ErrNoEncontrado), errors.Is(err, spotify.ErrNoEncontrado):
		return "No encontré tu playlist en " + nombre + " (¿la borraste?). Crea una nueva."
	case errors.Is(err, pool.ErrNoEncontrado):
		return "Primero crea tu playlist."
	default:
		s.log.Error("acción de plataforma fallida", "plataforma", p, "err", err)
		return "No se pudo hablar con " + nombre + ". Intenta de nuevo."
	}
}

// avisoDeURL convierte los parámetros de una redirección en un mensaje. Solo
// usa textos fijos y números: nunca refleja texto de la URL.
func avisoDeURL(q url.Values) aviso {
	nombre := conexion.Nombre[q.Get("p")]
	if nombre == "" {
		nombre = "la plataforma"
	}
	switch q.Get("aviso") {
	case "conectado":
		return aviso{Mensaje: nombre + " conectado."}
	case "desconectado":
		msg := nombre + " desconectado. Tu playlist sigue en tu cuenta, pero ya no se actualiza."
		if q.Get("p") == conexion.Spotify {
			msg += " Para quitar el acceso por completo, ve a spotify.com/account/apps."
		}
		return aviso{Mensaje: msg}
	case "cancelado":
		return aviso{Error: "Cancelaste la conexión con " + nombre + "."}
	case "sin-canal":
		return aviso{Error: "Tu cuenta de Google no tiene canal de YouTube. Crea uno en youtube.com y vuelve a intentar."}
	case "no-autorizado":
		return aviso{Error: "Tu cuenta de Spotify no está autorizada en la app (modo desarrollo: máximo 5 personas). Pide que la agreguen en el panel de Spotify for Developers."}
	case "sin-permiso":
		return aviso{Error: "Multiparty necesita todos los permisos que pide " + nombre + " para crear y actualizar tu playlist."}
	case "en-uso":
		return aviso{Error: "Esa cuenta de " + nombre + " ya está conectada por otra persona."}
	case "error":
		return aviso{Error: "No se pudo conectar con " + nombre + ". Intenta de nuevo."}
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

// ---- importar desde playlists ----

type itemImportar struct {
	ID      string
	Titulo  string
	Detalle string
}

type playlistImportar struct {
	ID       string
	Titulo   string
	Cantidad int
}

type datosImportar struct {
	Grupo          pool.Grupo
	Plataforma     string
	Nombre         string
	Playlists      []playlistImportar
	Items          []itemImportar
	PlaylistID     string
	PlaylistTitulo string
	Max            int
	Error          string
}

func (s *Server) importarVer(w http.ResponseWriter, r *http.Request) {
	g, uid, ok := s.requerirMiembro(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	p := q.Get("de")
	if p == "" {
		p = conexion.YouTube
	}
	if !s.con.Habilitada(p) {
		http.NotFound(w, r)
		return
	}
	d := datosImportar{Grupo: g, Plataforma: p, Nombre: conexion.Nombre[p], Max: maxImportar}
	id := q.Get("playlist")
	if id != "" && !idPlataforma.MatchString(id) {
		http.Error(w, "Playlist inválida.", http.StatusBadRequest)
		return
	}
	d.PlaylistID, d.PlaylistTitulo = id, q.Get("t")

	var err error
	switch p {
	case conexion.YouTube:
		yt, guardar, errC := s.con.YouTube(r.Context(), uid)
		if err = errC; err != nil {
			break
		}
		defer guardar()
		if id == "" {
			var pls []youtube.Playlist
			pls, err = yt.MisPlaylists(r.Context())
			for _, pl := range pls {
				d.Playlists = append(d.Playlists, playlistImportar{pl.ID, pl.Titulo, pl.Cantidad})
			}
		} else {
			var vs []youtube.Video
			vs, err = yt.ItemsDePlaylist(r.Context(), id, 200)
			for _, v := range vs {
				d.Items = append(d.Items, itemImportar{v.ID, v.Titulo, v.Canal})
			}
		}
	case conexion.Spotify:
		sp, guardar, errC := s.con.Spotify(r.Context(), uid)
		if err = errC; err != nil {
			break
		}
		defer guardar()
		if id == "" {
			var pls []spotify.Playlist
			pls, err = sp.MisPlaylists(r.Context())
			for _, pl := range pls {
				d.Playlists = append(d.Playlists, playlistImportar{pl.ID, pl.Titulo, pl.Cantidad})
			}
		} else {
			var ts []spotify.Track
			ts, err = sp.ItemsDePlaylist(r.Context(), id, 200)
			for _, t := range ts {
				d.Items = append(d.Items, itemImportar{t.ID, t.Titulo, t.Artista})
			}
		}
	}
	if err != nil {
		d.Error = s.mensajePlataforma(r.Context(), uid, p, err)
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
	p := r.PostForm.Get("de")
	if !s.con.Habilitada(p) {
		http.NotFound(w, r)
		return
	}
	var ids []string
	for _, id := range r.PostForm["v"] {
		if idPlataforma.MatchString(id) && len(ids) < maxImportar {
			ids = append(ids, id)
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	var canciones []pool.CancionResuelta
	var errs []error
	switch p {
	case conexion.YouTube:
		canciones, errs = s.resolverEnParalelo(ctx, ids, "https://music.youtube.com/watch?v=")
	case conexion.Spotify:
		canciones, errs = s.cancionesDeSpotify(ctx, uid, r.PostForm.Get("playlist"), ids)
	}

	var importadas, repetidas, fallidas int
	for i := range canciones {
		err := errs[i]
		if err == nil {
			err = s.store.AgregarCancion(r.Context(), g.ID, uid, canciones[i], pool.Importada)
		}
		var dup pool.ErrDuplicada
		switch {
		case err == nil:
			importadas++
		case errors.As(err, &dup):
			repetidas++
		default:
			fallidas++
			s.log.Info("no se pudo importar", "plataforma", p, "id", ids[i], "err", err)
		}
	}
	http.Redirect(w, r, fmt.Sprintf("/g/%s?importadas=%d&repetidas=%d&fallidas=%d",
		g.Codigo, importadas, repetidas, fallidas), http.StatusSeeOther)
}

// resolverEnParalelo resuelve links (4 a la vez) y conserva el orden.
func (s *Server) resolverEnParalelo(ctx context.Context, ids []string, prefijo string) ([]pool.CancionResuelta, []error) {
	out := make([]pool.CancionResuelta, len(ids))
	errs := make([]error, len(ids))
	var wg sync.WaitGroup
	turno := make(chan struct{}, 4)
	for i, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			turno <- struct{}{}
			defer func() { <-turno }()
			out[i], errs[i] = s.resolver.Resolver(ctx, prefijo+id)
		}()
	}
	wg.Wait()
	return out, errs
}

// cancionesDeSpotify arma las canciones con los datos de la API (incluye
// ISRC), sin consultar fuentes externas. Vuelve a leer la playlist para no
// confiar en datos del formulario.
func (s *Server) cancionesDeSpotify(ctx context.Context, uid int64, playlistID string, ids []string) ([]pool.CancionResuelta, []error) {
	out := make([]pool.CancionResuelta, len(ids))
	errs := make([]error, len(ids))
	falla := func(err error) ([]pool.CancionResuelta, []error) {
		for i := range errs {
			errs[i] = err
		}
		return out, errs
	}
	if !idPlataforma.MatchString(playlistID) {
		return falla(pool.ErrNoEncontrada)
	}
	sp, guardar, err := s.con.Spotify(ctx, uid)
	if err != nil {
		return falla(err)
	}
	defer guardar()
	tracks, err := sp.ItemsDePlaylist(ctx, playlistID, 200)
	if err != nil {
		return falla(err)
	}
	porID := map[string]spotify.Track{}
	for _, t := range tracks {
		porID[t.ID] = t
	}
	for i, id := range ids {
		t, ok := porID[id]
		if !ok {
			errs[i] = pool.ErrNoEncontrada
			continue
		}
		out[i] = pool.CancionResuelta{
			ISRC: t.ISRC, Titulo: t.Titulo, Artista: t.Artista, PortadaURL: t.Portada,
			DuracionMS: t.DuracionS * 1000,
			Enlaces: []pool.Enlace{
				{Plataforma: "spotify", TrackID: t.ID, URL: t.URL(), Confianza: pool.Directo},
				{Plataforma: "songlink", URL: "https://song.link/" + t.URL(), Confianza: pool.Directo},
			},
		}
	}
	return out, errs
}
