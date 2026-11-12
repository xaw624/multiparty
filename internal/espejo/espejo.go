// Paquete espejo sincroniza la playlist de un miembro en su plataforma con
// su ventana de 10 canciones.
//
// Por la política de YouTube, una sincronización solo ocurre por una acción
// del propio usuario (crear la playlist, "Ya la escuché" o "Sincronizar").
package espejo

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"

	"github.com/xaw624/multiparty/internal/pool"
	"github.com/xaw624/multiparty/internal/texto"
	"github.com/xaw624/multiparty/internal/youtube"
)

// API es lo que el espejo necesita de la plataforma (hoy, YouTube).
type API interface {
	AgregarItem(ctx context.Context, playlistID, videoID string) (string, error)
	QuitarItem(ctx context.Context, itemID string) error
	Buscar(ctx context.Context, consulta string, max int) ([]youtube.Video, error)
	Duraciones(ctx context.Context, ids []string) (map[string]int, error)
}

// Resultado resume una sincronización.
type Resultado struct {
	Agregadas int
	Quitadas  int
	Omitidas  int // canciones sin coincidencia en la plataforma
}

// Tolerancias de duración, en segundos, al emparejar con un video.
const (
	toleranciaTopic = 5  // pistas de YouTube Music: misma grabación
	toleranciaVideo = 20 // videos oficiales: pueden tener intro o final
	candidatos      = 5
)

var candados sync.Map // miembroID → *sync.Mutex

func candado(miembroID int64) *sync.Mutex {
	m, _ := candados.LoadOrStore(miembroID, &sync.Mutex{})
	return m.(*sync.Mutex)
}

// Sincronizar quita de la playlist lo escuchado y agrega lo que falta de la
// ventana. Si se agota la cuota a medias, devuelve lo hecho y el error; la
// siguiente sincronización continúa donde quedó.
func Sincronizar(ctx context.Context, st *pool.Store, api API, grupoID, usuarioID int64) (Resultado, error) {
	var res Resultado
	plan, err := st.PlanDeEspejo(ctx, grupoID, usuarioID)
	if err != nil {
		return res, err
	}
	mu := candado(plan.Espejo.MiembroID)
	mu.Lock()
	defer mu.Unlock()
	// Recalcular dentro del candado: otra sincronización pudo adelantarse.
	if plan, err = st.PlanDeEspejo(ctx, grupoID, usuarioID); err != nil {
		return res, err
	}

	for _, q := range plan.Quitar {
		if err := api.QuitarItem(ctx, q.ItemID); err != nil {
			return res, err
		}
		if err := st.ItemQuitado(ctx, plan.Espejo.MiembroID, q.Posicion); err != nil {
			return res, err
		}
		res.Quitadas++
	}

	for _, c := range plan.Agregar {
		if c.SinCoincidencia {
			res.Omitidas++
			continue
		}
		videoID := c.TrackID
		if videoID == "" {
			videoID, err = emparejar(ctx, st, api, c)
			if err != nil {
				return res, err
			}
			if videoID == "" {
				res.Omitidas++
				continue
			}
		}
		itemID, err := api.AgregarItem(ctx, plan.Espejo.PlaylistID, videoID)
		if err != nil {
			return res, err
		}
		if err := st.ItemAgregado(ctx, plan.Espejo.MiembroID, c.Posicion, itemID); err != nil {
			return res, err
		}
		res.Agregadas++
	}
	return res, nil
}

// emparejar busca la canción en YouTube (100 unidades + 1 por duraciones) y
// guarda el resultado. Devuelve "" si no hay una coincidencia confiable.
func emparejar(ctx context.Context, st *pool.Store, api API, c pool.CancionEspejo) (string, error) {
	videos, err := api.Buscar(ctx, c.Artista+" "+texto.Normalizar(c.Titulo), candidatos)
	if err != nil {
		return "", err
	}
	elegido, err := elegir(ctx, api, c, videos)
	if err != nil {
		return "", err
	}
	if elegido == "" {
		j, _ := json.Marshal(videos)
		return "", st.SinCoincidencia(ctx, c.CancionID, "youtube", string(j))
	}
	err = st.GuardarEnlaces(ctx, c.CancionID, []pool.Enlace{
		{Plataforma: "youtubeMusic", TrackID: elegido, URL: "https://music.youtube.com/watch?v=" + elegido, Confianza: pool.Busqueda},
		{Plataforma: "youtube", TrackID: elegido, URL: "https://www.youtube.com/watch?v=" + elegido, Confianza: pool.Busqueda},
	})
	return elegido, err
}

// elegir prefiere pistas de canales "Artista - Topic" (las de YouTube Music)
// y exige el mismo título, el mismo artista y una duración parecida.
func elegir(ctx context.Context, api API, c pool.CancionEspejo, videos []youtube.Video) (string, error) {
	var ok []youtube.Video
	for _, v := range videos {
		t, a := texto.LimpiarYouTube(v.Titulo, v.Canal)
		if texto.MismoTitulo(t, c.Titulo) && texto.MismoArtista(a, c.Artista) {
			ok = append(ok, v)
		}
	}
	if len(ok) == 0 {
		return "", nil
	}
	sort.SliceStable(ok, func(i, j int) bool { return esTopic(ok[i]) && !esTopic(ok[j]) })
	if c.DuracionS == 0 {
		return ok[0].ID, nil
	}

	ids := make([]string, len(ok))
	for i, v := range ok {
		ids[i] = v.ID
	}
	dur, err := api.Duraciones(ctx, ids)
	if err != nil {
		if errors.Is(err, youtube.ErrCuota) {
			return "", err
		}
		return ok[0].ID, nil // sin duraciones, el mejor por título y canal
	}
	for _, v := range ok {
		d, conocida := dur[v.ID]
		tol := toleranciaVideo
		if esTopic(v) {
			tol = toleranciaTopic
		}
		if conocida && abs(d-c.DuracionS) <= tol {
			return v.ID, nil
		}
	}
	return "", nil
}

func esTopic(v youtube.Video) bool { return strings.HasSuffix(v.Canal, " - Topic") }

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
