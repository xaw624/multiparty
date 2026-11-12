package espejo

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/xaw624/multiparty/internal/pool"
	"github.com/xaw624/multiparty/internal/spotify"
	"github.com/xaw624/multiparty/internal/texto"
)

// APISpotify es lo que el espejo necesita de Spotify.
type APISpotify interface {
	Reemplazar(ctx context.Context, playlistID string, uris []string) error
	Buscar(ctx context.Context, consulta string, limite int) ([]spotify.Track, error)
}

// SincronizarSpotify deja la playlist con exactamente la ventana, en orden,
// con una sola llamada (Spotify no tiene cuota diaria por unidades).
func SincronizarSpotify(ctx context.Context, st *pool.Store, api APISpotify, grupoID, usuarioID int64) (Resultado, error) {
	var res Resultado
	plan, err := st.PlanDeEspejo(ctx, grupoID, usuarioID)
	if err != nil {
		return res, err
	}
	mu := candado(plan.Espejo.MiembroID)
	mu.Lock()
	defer mu.Unlock()
	if plan, err = st.PlanDeEspejo(ctx, grupoID, usuarioID); err != nil {
		return res, err
	}

	faltaban := map[int]bool{}
	for _, a := range plan.Agregar {
		faltaban[a.Posicion] = true
	}

	var uris []string
	items := map[int]string{}
	for _, c := range plan.Ventana {
		trackID := c.TrackID
		if trackID == "" && !c.SinCoincidencia {
			if trackID, err = EmparejarSpotify(ctx, st, api, c); err != nil {
				return res, err
			}
		}
		if trackID == "" {
			res.Omitidas++
			continue
		}
		uri := "spotify:track:" + trackID
		uris = append(uris, uri)
		items[c.Posicion] = uri
		if faltaban[c.Posicion] {
			res.Agregadas++
		}
	}
	if err := api.Reemplazar(ctx, plan.Espejo.PlaylistID, uris); err != nil {
		return Resultado{}, err
	}
	res.Quitadas = len(plan.Quitar)
	return res, st.EspejoReemplazado(ctx, plan.Espejo.MiembroID, items)
}

// EmparejarSpotify busca la canción en Spotify: primero por ISRC (misma
// grabación) y si no, por título, artista y duración. Guarda el resultado y
// devuelve "" si no hay coincidencia confiable.
func EmparejarSpotify(ctx context.Context, st *pool.Store, api APISpotify, c pool.CancionEspejo) (string, error) {
	var vistos []spotify.Track
	if c.ISRC != "" {
		ts, err := api.Buscar(ctx, "isrc:"+c.ISRC, 5)
		if err != nil {
			return "", err
		}
		if len(ts) > 0 {
			return ts[0].ID, guardarSpotify(ctx, st, c.CancionID, ts[0], pool.ISRC)
		}
	}

	consultas := []string{
		fmt.Sprintf("track:%q artist:%q", texto.Normalizar(c.Titulo), c.Artista),
		c.Artista + " " + texto.Normalizar(c.Titulo),
	}
	for _, q := range consultas {
		ts, err := api.Buscar(ctx, q, spotify.MaxBusqueda)
		if err != nil {
			return "", err
		}
		vistos = append(vistos, ts...)
		for _, t := range ts {
			if !texto.MismoTitulo(t.Titulo, c.Titulo) || !texto.MismoArtista(t.Artista, c.Artista) {
				continue
			}
			if c.DuracionS > 0 && t.DuracionS > 0 && abs(t.DuracionS-c.DuracionS) > toleranciaTopic {
				continue
			}
			return t.ID, guardarSpotify(ctx, st, c.CancionID, t, pool.Busqueda)
		}
	}

	cands := make([]pool.Candidato, 0, len(vistos))
	for _, t := range vistos {
		cands = append(cands, pool.Candidato{ID: t.ID, Titulo: t.Titulo, Detalle: t.Artista, URL: t.URL()})
		if len(cands) == 5 {
			break
		}
	}
	j, _ := json.Marshal(cands)
	return "", st.SinCoincidencia(ctx, c.CancionID, "spotify", string(j))
}

func guardarSpotify(ctx context.Context, st *pool.Store, cancionID int64, t spotify.Track, conf pool.Confianza) error {
	return st.GuardarEnlaces(ctx, cancionID, []pool.Enlace{
		{Plataforma: "spotify", TrackID: t.ID, URL: t.URL(), Confianza: conf},
	})
}
