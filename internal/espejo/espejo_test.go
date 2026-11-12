package espejo

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/xaw624/multiparty/internal/db"
	"github.com/xaw624/multiparty/internal/pool"
	"github.com/xaw624/multiparty/internal/youtube"
)

type apiFalsa struct {
	busquedas  map[string][]youtube.Video // por artista
	duraciones map[string]int
	agregados  []string // videoIDs
	quitados   []string // itemIDs
	nBusquedas int
	cuotaEn    int // falla AgregarItem al llegar a este número de agregados (0 = nunca)
}

func (a *apiFalsa) AgregarItem(_ context.Context, _, videoID string) (string, error) {
	if a.cuotaEn > 0 && len(a.agregados)+1 == a.cuotaEn {
		return "", youtube.ErrCuota
	}
	a.agregados = append(a.agregados, videoID)
	return "item-" + videoID, nil
}

func (a *apiFalsa) QuitarItem(_ context.Context, itemID string) error {
	a.quitados = append(a.quitados, itemID)
	return nil
}

func (a *apiFalsa) Buscar(_ context.Context, consulta string, _ int) ([]youtube.Video, error) {
	a.nBusquedas++
	for artista, v := range a.busquedas {
		if len(consulta) >= len(artista) && consulta[:len(artista)] == artista {
			return v, nil
		}
	}
	return nil, nil
}

func (a *apiFalsa) Duraciones(_ context.Context, ids []string) (map[string]int, error) {
	out := map[string]int{}
	for _, id := range ids {
		if d, ok := a.duraciones[id]; ok {
			out[id] = d
		}
	}
	return out, nil
}

func cancion(titulo, artista string, durS int, enlaces ...pool.Enlace) pool.CancionResuelta {
	enlaces = append(enlaces, pool.Enlace{Plataforma: "deezer", TrackID: "dz-" + titulo, URL: "https://deezer.test/" + titulo})
	return pool.CancionResuelta{Titulo: titulo, Artista: artista, DuracionMS: durS * 1000, Enlaces: enlaces}
}

func debe(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func preparar(t *testing.T) (*pool.Store, int64, int64) {
	t.Helper()
	ctx := context.Background()
	database, err := db.Open(filepath.Join(t.TempDir(), "e.db"))
	debe(t, err)
	t.Cleanup(func() { database.Close() })
	debe(t, db.Migrate(ctx, database))
	st := pool.NewStore(database)

	ana, _ := st.CrearUsuario(ctx, "Ana")
	g, err := st.CrearGrupo(ctx, "Amigos", ana)
	debe(t, err)
	// S1 ya trae su id de YouTube; S2 hay que buscarla; S3 no existe en YouTube.
	debe(t, st.AgregarCancion(ctx, g.ID, ana, cancion("Uno", "Banda", 200,
		pool.Enlace{Plataforma: "youtubeMusic", TrackID: "v1", URL: "https://music.youtube.com/watch?v=v1", Confianza: pool.Directo}), pool.Suelta))
	debe(t, st.AgregarCancion(ctx, g.ID, ana, cancion("Dos", "Solista", 180), pool.Suelta))
	debe(t, st.AgregarCancion(ctx, g.ID, ana, cancion("Tres", "Rara", 150), pool.Suelta))
	debe(t, st.CrearEspejo(ctx, g.ID, ana, "youtube", "PL1"))
	return st, g.ID, ana
}

func TestSincronizar(t *testing.T) {
	ctx := context.Background()
	st, g, ana := preparar(t)
	api := &apiFalsa{
		busquedas: map[string][]youtube.Video{
			"Solista": {
				{ID: "vid-oficial", Titulo: "Solista - Dos (Official Video)", Canal: "SolistaVEVO"},
				{ID: "v2", Titulo: "Dos", Canal: "Solista - Topic"},
				{ID: "otra", Titulo: "Dos (En Vivo)", Canal: "Fan"},
			},
			"Rara": {{ID: "x", Titulo: "Otra cosa", Canal: "Nadie"}},
		},
		duraciones: map[string]int{"vid-oficial": 230, "v2": 181},
	}

	plan, err := st.PlanDeEspejo(ctx, g, ana)
	debe(t, err)
	if plan.Pendientes() != 3 {
		t.Fatalf("pendientes antes = %d, quiere 3", plan.Pendientes())
	}

	res, err := SincronizarYouTube(ctx, st, api, g, ana)
	debe(t, err)
	if res != (Resultado{Agregadas: 2, Omitidas: 1}) {
		t.Errorf("resultado = %+v", res)
	}
	// Prefiere la pista "Topic" y respeta el orden de la ventana.
	if fmt.Sprint(api.agregados) != "[v1 v2]" {
		t.Errorf("agregados = %v", api.agregados)
	}

	v, err := st.Vista(ctx, g, ana)
	debe(t, err)
	if v.Pendientes != 0 || !v.Ventana[0].EnPlaylist || !v.Ventana[2].SinCoincidencia {
		t.Errorf("vista: pendientes=%d, ventana=%+v", v.Pendientes, v.Ventana)
	}

	// Una segunda sincronización no vuelve a buscar la que no se encontró.
	antes := api.nBusquedas
	_, err = SincronizarYouTube(ctx, st, api, g, ana)
	debe(t, err)
	if api.nBusquedas != antes {
		t.Error("no debería volver a buscar canciones sin coincidencia")
	}

	// Al escucharla, sale de la playlist.
	debe(t, st.MarcarEscuchada(ctx, g, ana, 0))
	res, err = SincronizarYouTube(ctx, st, api, g, ana)
	debe(t, err)
	if res.Quitadas != 1 || fmt.Sprint(api.quitados) != "[item-v1]" {
		t.Errorf("quitar: res=%+v quitados=%v", res, api.quitados)
	}
}

func TestSincronizarSeDetieneSinCuota(t *testing.T) {
	ctx := context.Background()
	st, g, ana := preparar(t)
	api := &apiFalsa{
		busquedas:  map[string][]youtube.Video{"Solista": {{ID: "v2", Titulo: "Dos", Canal: "Solista - Topic"}}},
		duraciones: map[string]int{"v2": 180},
		cuotaEn:    2,
	}
	res, err := SincronizarYouTube(ctx, st, api, g, ana)
	if !errors.Is(err, youtube.ErrCuota) || res.Agregadas != 1 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	// Al día siguiente continúa donde quedó, sin repetir lo ya agregado.
	api.cuotaEn = 0
	res, err = SincronizarYouTube(ctx, st, api, g, ana)
	debe(t, err)
	if res.Agregadas != 1 || fmt.Sprint(api.agregados) != "[v1 v2]" {
		t.Errorf("continuación: res=%+v agregados=%v", res, api.agregados)
	}
}
