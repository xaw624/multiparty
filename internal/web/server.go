// Paquete web contiene las rutas HTTP, las plantillas y los archivos estáticos.
package web

import (
	"bytes"
	"database/sql"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
)

//go:embed templates/*.html static/*
var assets embed.FS

// páginas que se renderizan dentro de layout.html.
var pages = []string{"index.html"}

// Server agrupa las dependencias de los handlers.
type Server struct {
	db    *sql.DB
	log   *slog.Logger
	pages map[string]*template.Template
}

// New prepara el servidor y compila las plantillas.
func New(db *sql.DB, log *slog.Logger) (*Server, error) {
	s := &Server{db: db, log: log, pages: map[string]*template.Template{}}
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
	mux.HandleFunc("GET /{$}", s.index)

	return mux
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	if err := s.db.PingContext(r.Context()); err != nil {
		http.Error(w, "db no disponible", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintln(w, "ok")
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	s.render(w, "index.html", nil)
}

// render ejecuta la página en un búfer para no enviar HTML a medias si falla.
func (s *Server) render(w http.ResponseWriter, page string, data any) {
	t, ok := s.pages[page]
	if !ok {
		s.log.Error("página desconocida", "page", page)
		http.Error(w, "error interno", http.StatusInternalServerError)
		return
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "layout", data); err != nil {
		s.log.Error("render", "page", page, "err", err)
		http.Error(w, "error interno", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = buf.WriteTo(w)
}
