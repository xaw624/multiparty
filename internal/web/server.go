// Paquete web contiene las rutas HTTP, las plantillas y los archivos estáticos.
package web

import (
	"bytes"
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/oauth2"

	"github.com/xaw624/multiparty/internal/cifrado"
	"github.com/xaw624/multiparty/internal/conexion"
	"github.com/xaw624/multiparty/internal/pool"
)

//go:embed templates/*.html static/*
var assets embed.FS

// páginas que se renderizan dentro de layout.html.
var pages = []string{"index.html", "grupo.html", "unirse.html", "importar.html"}

// Resolver convierte un link pegado por el usuario en una canción.
type Resolver interface {
	Resolver(ctx context.Context, enlace string) (pool.CancionResuelta, error)
}

// Opciones configura el servidor.
type Opciones struct {
	Resolver Resolver
	// Secreto firma las cookies de sesión.
	Secreto []byte
	// Google habilita YouTube Music y Spotify habilita Spotify; nil los deshabilita.
	Google  *oauth2.Config
	Spotify *oauth2.Config
	// Caja cifra los tokens OAuth; obligatoria con Google o Spotify.
	Caja *cifrado.Caja
	// Reemplazan las URL de las API (pruebas).
	YouTubeBase string
	SpotifyBase string
	RevocarURL  string
}

// Server agrupa las dependencias de los handlers.
type Server struct {
	db         *sql.DB
	store      *pool.Store
	resolver   Resolver
	ses        sesiones
	con        *conexion.Conexiones
	revocarURL string
	log        *slog.Logger
	pages      map[string]*template.Template
}

// New prepara el servidor y compila las plantillas.
func New(db *sql.DB, log *slog.Logger, op Opciones) (*Server, error) {
	store := pool.NewStore(db)
	con, err := conexion.New(store, log, conexion.Opciones{
		Google: op.Google, Spotify: op.Spotify, Caja: op.Caja,
		Bases: map[string]string{conexion.YouTube: op.YouTubeBase, conexion.Spotify: op.SpotifyBase},
	})
	if err != nil {
		return nil, err
	}
	s := &Server{
		db:         db,
		store:      store,
		resolver:   op.Resolver,
		ses:        sesiones{clave: op.Secreto},
		con:        con,
		revocarURL: op.RevocarURL,
		log:        log,
		pages:      map[string]*template.Template{},
	}
	if s.revocarURL == "" {
		s.revocarURL = "https://oauth2.googleapis.com/revoke"
	}
	for _, p := range pages {
		t, err := template.ParseFS(assets, "templates/layout.html", "templates/"+p)
		if err != nil {
			return nil, fmt.Errorf("plantilla %s: %w", p, err)
		}
		s.pages[p] = t
	}
	return s, nil
}

// Routes devuelve el enrutador con todas las rutas registradas.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	static, err := fs.Sub(assets, "static")
	if err != nil {
		panic(err) // solo falla si cambia la directiva embed
	}
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))

	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /{$}", s.inicio)
	mux.HandleFunc("POST /grupos", s.crearGrupo)
	mux.HandleFunc("GET /g/{codigo}", s.verGrupo)
	mux.HandleFunc("POST /g/{codigo}/unirse", s.unirse)
	mux.HandleFunc("POST /g/{codigo}/aportes", s.agregar)
	mux.HandleFunc("POST /g/{codigo}/aportes/{id}/eliminar", s.eliminar)
	mux.HandleFunc("POST /g/{codigo}/escuchada/{pos}", s.escuchada)
	mux.HandleFunc("POST /g/{codigo}/preferencia", s.preferencia)

	// Cuentas de YouTube y Spotify (F2, F3).
	mux.HandleFunc("GET /cuentas/{plataforma}/conectar", s.conectar)
	mux.HandleFunc("GET /cuentas/{plataforma}/callback", s.callback)
	mux.HandleFunc("POST /cuentas/{plataforma}/desconectar", s.desconectar)
	mux.HandleFunc("POST /g/{codigo}/espejo/{plataforma}", s.crearEspejo)
	mux.HandleFunc("POST /g/{codigo}/sincronizar", s.sincronizarHandler)
	mux.HandleFunc("GET /g/{codigo}/importar", s.importarVer)
	mux.HandleFunc("POST /g/{codigo}/importar", s.importar)

	// Rechaza POST desde otros sitios (CSRF) con Sec-Fetch-Site / Origin.
	return http.NewCrossOriginProtection().Handler(mux)
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	if err := s.db.PingContext(r.Context()); err != nil {
		http.Error(w, "db no disponible", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintln(w, "ok")
}

// ---- inicio y grupos ----

type datosInicio struct {
	Nombre string
	Grupos []pool.Grupo
	Error  string
}

func (s *Server) inicio(w http.ResponseWriter, r *http.Request) {
	s.renderInicio(w, r, "")
}

func (s *Server) renderInicio(w http.ResponseWriter, r *http.Request, msg string) {
	d := datosInicio{Error: msg}
	if uid, nombre, ok := s.usuarioActual(r); ok {
		d.Nombre = nombre
		grupos, err := s.store.GruposDeUsuario(r.Context(), uid)
		if err != nil {
			s.fallo(w, err)
			return
		}
		d.Grupos = grupos
	}
	s.render(w, "index.html", d)
}

func (s *Server) crearGrupo(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	nombreGrupo, ok := limpiar(r.FormValue("nombre_grupo"), 60)
	if !ok {
		s.renderInicio(w, r, "El nombre del grupo debe tener entre 1 y 60 caracteres.")
		return
	}
	uid, msg, err := s.asegurarUsuario(w, r)
	if err != nil {
		s.fallo(w, err)
		return
	}
	if msg != "" {
		s.renderInicio(w, r, msg)
		return
	}
	g, err := s.store.CrearGrupo(r.Context(), nombreGrupo, uid)
	if err != nil {
		s.fallo(w, err)
		return
	}
	http.Redirect(w, r, "/g/"+g.Codigo, http.StatusSeeOther)
}

type datosUnirse struct {
	Grupo  pool.Grupo
	Nombre string
	Error  string
}

func (s *Server) verGrupo(w http.ResponseWriter, r *http.Request) {
	g, ok := s.grupo(w, r)
	if !ok {
		return
	}
	uid, nombre, conSesion := s.usuarioActual(r)
	if conSesion {
		miembro, err := s.store.EsMiembro(r.Context(), g.ID, uid)
		if err != nil {
			s.fallo(w, err)
			return
		}
		if miembro {
			s.renderGrupo(w, r, g, uid, avisoDeURL(r.URL.Query()), false)
			return
		}
	}
	s.render(w, "unirse.html", datosUnirse{Grupo: g, Nombre: nombre})
}

func (s *Server) unirse(w http.ResponseWriter, r *http.Request) {
	g, ok := s.grupo(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	uid, msg, err := s.asegurarUsuario(w, r)
	if err != nil {
		s.fallo(w, err)
		return
	}
	if msg != "" {
		s.render(w, "unirse.html", datosUnirse{Grupo: g, Error: msg})
		return
	}
	if err := s.store.Unirse(r.Context(), g.ID, uid); err != nil {
		s.fallo(w, err)
		return
	}
	http.Redirect(w, r, "/g/"+g.Codigo, http.StatusSeeOther)
}

// ---- acciones dentro de un grupo ----

func (s *Server) agregar(w http.ResponseWriter, r *http.Request) {
	g, uid, ok := s.requerirMiembro(w, r)
	if !ok {
		return
	}
	enlace := strings.TrimSpace(r.FormValue("enlace"))
	c, err := s.resolver.Resolver(r.Context(), enlace)
	if err == nil {
		err = s.store.AgregarCancion(r.Context(), g.ID, uid, c, pool.Suelta)
	}
	if err != nil {
		s.responder(w, r, g, uid, aviso{Error: s.mensajeError(err), Enlace: enlace})
		return
	}
	s.responder(w, r, g, uid, aviso{Mensaje: "Agregaste «" + c.Titulo + "»."})
}

func (s *Server) eliminar(w http.ResponseWriter, r *http.Request) {
	g, uid, ok := s.requerirMiembro(w, r)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err == nil {
		err = s.store.EliminarAporte(r.Context(), g.ID, uid, id)
	}
	s.responder(w, r, g, uid, aviso{Error: s.mensajeError(err)})
}

func (s *Server) escuchada(w http.ResponseWriter, r *http.Request) {
	g, uid, ok := s.requerirMiembro(w, r)
	if !ok {
		return
	}
	pos, err := strconv.Atoi(r.PathValue("pos"))
	if err == nil {
		err = s.store.MarcarEscuchada(r.Context(), g.ID, uid, pos)
	}
	if err != nil {
		s.responder(w, r, g, uid, aviso{Error: s.mensajeError(err)})
		return
	}
	// Con playlist espejo, la acción del usuario también la actualiza.
	a := aviso{}
	if e, err := s.store.EspejoDe(r.Context(), g.ID, uid); err == nil && s.con.Habilitada(e.Plataforma) {
		a = s.sincronizar(r.Context(), g, uid, false)
	}
	s.responder(w, r, g, uid, a)
}

func (s *Server) preferencia(w http.ResponseWriter, r *http.Request) {
	g, uid, ok := s.requerirMiembro(w, r)
	if !ok {
		return
	}
	err := s.store.CambiarPlataformaLinks(r.Context(), g.ID, uid, r.FormValue("plataforma"))
	s.responder(w, r, g, uid, aviso{Error: s.mensajeError(err)})
}

// ---- utilidades ----

type aviso struct {
	Error   string
	Mensaje string
	Enlace  string // se devuelve al formulario si hubo error
}

type datosGrupo struct {
	pool.Vista
	Error             string
	Mensaje           string
	Enlace            string
	InvitarURL        string
	YouTubeDisponible bool
	SpotifyDisponible bool
}

// responder devuelve el fragmento #grupo a HTMX, o redirige si es un
// formulario normal y no hubo error.
func (s *Server) responder(w http.ResponseWriter, r *http.Request, g pool.Grupo, uid int64, a aviso) {
	if r.Header.Get("HX-Request") == "true" {
		s.renderGrupo(w, r, g, uid, a, true)
		return
	}
	if a.Error == "" {
		http.Redirect(w, r, "/g/"+g.Codigo, http.StatusSeeOther)
		return
	}
	s.renderGrupo(w, r, g, uid, a, false)
}

func (s *Server) renderGrupo(w http.ResponseWriter, r *http.Request, g pool.Grupo, uid int64, a aviso, parcial bool) {
	v, err := s.store.Vista(r.Context(), g.ID, uid)
	if err != nil {
		s.fallo(w, err)
		return
	}
	scheme := "http"
	if esHTTPS(r) {
		scheme = "https"
	}
	d := datosGrupo{
		Vista:             v,
		Error:             a.Error,
		Mensaje:           a.Mensaje,
		Enlace:            a.Enlace,
		InvitarURL:        scheme + "://" + r.Host + "/g/" + g.Codigo,
		YouTubeDisponible: s.con.Habilitada(conexion.YouTube),
		SpotifyDisponible: s.con.Habilitada(conexion.Spotify),
	}
	if parcial {
		s.ejecutar(w, "grupo.html", "grupo", d)
		return
	}
	s.render(w, "grupo.html", d)
}

// grupo busca el grupo de la ruta; si no existe responde 404.
func (s *Server) grupo(w http.ResponseWriter, r *http.Request) (pool.Grupo, bool) {
	g, err := s.store.GrupoPorCodigo(r.Context(), r.PathValue("codigo"))
	if errors.Is(err, pool.ErrNoEncontrado) {
		http.NotFound(w, r)
		return g, false
	}
	if err != nil {
		s.fallo(w, err)
		return g, false
	}
	return g, true
}

// requerirMiembro valida que quien hace la petición pertenece al grupo.
func (s *Server) requerirMiembro(w http.ResponseWriter, r *http.Request) (pool.Grupo, int64, bool) {
	g, ok := s.grupo(w, r)
	if !ok {
		return g, 0, false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	uid, _, conSesion := s.usuarioActual(r)
	if conSesion {
		miembro, err := s.store.EsMiembro(r.Context(), g.ID, uid)
		if err != nil {
			s.fallo(w, err)
			return g, 0, false
		}
		if miembro {
			return g, uid, true
		}
	}
	http.Error(w, "Primero únete al grupo.", http.StatusForbidden)
	return g, 0, false
}

// usuarioActual lee la cookie y comprueba que el usuario exista.
func (s *Server) usuarioActual(r *http.Request) (int64, string, bool) {
	uid, ok := s.ses.leer(r)
	if !ok {
		return 0, "", false
	}
	nombre, err := s.store.NombreUsuario(r.Context(), uid)
	if err != nil {
		return 0, "", false
	}
	return uid, nombre, true
}

// asegurarUsuario devuelve el usuario de la sesión o crea uno con el campo
// "tu_nombre". Si el nombre no es válido devuelve un mensaje para mostrar.
func (s *Server) asegurarUsuario(w http.ResponseWriter, r *http.Request) (int64, string, error) {
	if uid, _, ok := s.usuarioActual(r); ok {
		return uid, "", nil
	}
	nombre, ok := limpiar(r.FormValue("tu_nombre"), 40)
	if !ok {
		return 0, "Tu nombre debe tener entre 1 y 40 caracteres.", nil
	}
	uid, err := s.store.CrearUsuario(r.Context(), nombre)
	if err != nil {
		return 0, "", err
	}
	s.ses.establecer(w, r, uid)
	return uid, "", nil
}

func (s *Server) mensajeError(err error) string {
	var dup pool.ErrDuplicada
	switch {
	case err == nil:
		return ""
	case errors.As(err, &dup):
		return "Esa canción ya está en el pool (la agregó " + dup.Por + ")."
	case errors.Is(err, pool.ErrEnlaceInvalido):
		return "Eso no parece un link. Copia el link de compartir de la canción."
	case errors.Is(err, pool.ErrNoSoportada):
		return "Por ahora se aceptan links de Spotify, YouTube, YouTube Music, Apple Music y Deezer."
	case errors.Is(err, pool.ErrNoEsCancion):
		return "Ese link no es de una canción (¿es un álbum o una playlist?)."
	case errors.Is(err, pool.ErrNoEncontrada):
		return "No encontré esa canción. Revisa el link."
	case errors.Is(err, pool.ErrLimite):
		return "Demasiadas búsquedas seguidas. Espera un minuto y vuelve a intentar."
	case errors.Is(err, pool.ErrNoPermitido), errors.Is(err, pool.ErrNoEncontrado):
		return "No se pudo hacer eso; quizá la página estaba desactualizada."
	default:
		s.log.Error("acción fallida", "err", err)
		return "Algo falló. Intenta de nuevo."
	}
}

func (s *Server) fallo(w http.ResponseWriter, err error) {
	s.log.Error("petición fallida", "err", err)
	http.Error(w, "Error interno.", http.StatusInternalServerError)
}

// limpiar recorta espacios y valida la longitud en caracteres.
func limpiar(v string, max int) (string, bool) {
	v = strings.Join(strings.Fields(v), " ")
	n := utf8.RuneCountInString(v)
	return v, n > 0 && n <= max
}

func (s *Server) render(w http.ResponseWriter, page string, data any) {
	s.ejecutar(w, page, "layout", data)
}

// ejecutar renderiza en un búfer para no enviar HTML a medias si falla.
func (s *Server) ejecutar(w http.ResponseWriter, page, nombre string, data any) {
	t, ok := s.pages[page]
	if !ok {
		s.fallo(w, fmt.Errorf("página desconocida %q", page))
		return
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, nombre, data); err != nil {
		s.fallo(w, fmt.Errorf("render %s/%s: %w", page, nombre, err))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = buf.WriteTo(w)
}
