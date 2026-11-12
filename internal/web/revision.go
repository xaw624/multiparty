package web

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/xaw624/multiparty/internal/conexion"
	"github.com/xaw624/multiparty/internal/pool"
	"github.com/xaw624/multiparty/internal/resolver"
)

type datosRevision struct {
	Grupo      pool.Grupo
	Revisiones []pool.Revision
	Nombres    map[string]string
	Error      string
	Mensaje    string
}

// revisionVer lista las canciones que no se emparejaron solas.
func (s *Server) revisionVer(w http.ResponseWriter, r *http.Request) {
	g, _, ok := s.requerirMiembro(w, r)
	if !ok {
		return
	}
	s.renderRevision(w, r, g, aviso{})
}

func (s *Server) renderRevision(w http.ResponseWriter, r *http.Request, g pool.Grupo, a aviso) {
	revs, err := s.store.RevisionesDeGrupo(r.Context(), g.ID)
	if err != nil {
		s.fallo(w, err)
		return
	}
	s.render(w, "revision.html", datosRevision{
		Grupo: g, Revisiones: revs, Nombres: conexion.Nombre, Error: a.Error, Mensaje: a.Mensaje,
	})
}

// revisionResolver aplica la decisión: elegir un candidato, pegar el link
// correcto o volver a buscar en la siguiente sincronización.
func (s *Server) revisionResolver(w http.ResponseWriter, r *http.Request) {
	g, uid, ok := s.requerirMiembro(w, r)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	rev, err := s.store.RevisionDeGrupo(r.Context(), g.ID, id)
	if errors.Is(err, pool.ErrNoEncontrado) {
		s.renderRevision(w, r, g, aviso{Error: "Esa revisión ya no está abierta."})
		return
	}
	if err != nil {
		s.fallo(w, err)
		return
	}

	var trackID string
	switch r.FormValue("accion") {
	case "reintentar":
		if err := s.store.ReintentarRevision(r.Context(), rev.ID); err != nil {
			s.fallo(w, err)
			return
		}
		s.renderRevision(w, r, g, aviso{Mensaje: "Se volverá a buscar «" + rev.Titulo + "» en la próxima sincronización."})
		return
	case "elegir":
		elegido := r.FormValue("track")
		for _, c := range rev.Candidatos {
			if c.ID == elegido {
				trackID = c.ID
			}
		}
	case "link":
		p, tid, err := resolver.Identificar(r.FormValue("enlace"))
		if err == nil && p == rev.Plataforma {
			trackID = tid
		} else {
			s.renderRevision(w, r, g, aviso{Error: "Pega un link de una canción de " + conexion.Nombre[rev.Plataforma] + "."})
			return
		}
	}
	if trackID == "" {
		s.renderRevision(w, r, g, aviso{Error: "Elige una opción."})
		return
	}

	var enlaces []pool.Enlace
	switch rev.Plataforma {
	case conexion.YouTube:
		enlaces = []pool.Enlace{
			{Plataforma: "youtubeMusic", TrackID: trackID, URL: "https://music.youtube.com/watch?v=" + trackID},
			{Plataforma: "youtube", TrackID: trackID, URL: "https://www.youtube.com/watch?v=" + trackID},
		}
	case conexion.Spotify:
		enlaces = []pool.Enlace{{Plataforma: "spotify", TrackID: trackID, URL: "https://open.spotify.com/track/" + trackID}}
	default:
		http.Error(w, "Plataforma desconocida.", http.StatusBadRequest)
		return
	}
	if err := s.store.ResolverRevision(r.Context(), rev.ID, uid, enlaces); err != nil {
		s.fallo(w, err)
		return
	}
	s.renderRevision(w, r, g, aviso{Mensaje: "Listo: «" + rev.Titulo + "» entrará a las playlists en la próxima sincronización."})
}
