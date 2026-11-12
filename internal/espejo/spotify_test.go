package espejo

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xaw624/multiparty/internal/db"
	"github.com/xaw624/multiparty/internal/pool"
	"github.com/xaw624/multiparty/internal/spotify"
)

type spotifyFalso struct {
	reemplazos [][]string
	consultas  []string
	resultados map[string][]spotify.Track // por prefijo de consulta
}

func (s *spotifyFalso) Reemplazar(_ context.Context, _ string, uris []string) error {
	s.reemplazos = append(s.reemplazos, uris)
	return nil
}

func (s *spotifyFalso) Buscar(_ context.Context, q string, _ int) ([]spotify.Track, error) {
	s.consultas = append(s.consultas, q)
	for pref, ts := range s.resultados {
		if strings.HasPrefix(q, pref) {
			return ts, nil
		}
	}
	return nil, nil
}

func TestSincronizarSpotify(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(filepath.Join(t.TempDir(), "s.db"))
	debe(t, err)
	t.Cleanup(func() { database.Close() })
	debe(t, db.Migrate(ctx, database))
	st := pool.NewStore(database)
	beto, _ := st.CrearUsuario(ctx, "Beto")
	g, err := st.CrearGrupo(ctx, "Amigos", beto)
	debe(t, err)

	// S1 vino de Spotify; S2 tiene ISRC; S3 hay que buscarla por texto; S4 no existe.
	debe(t, st.AgregarCancion(ctx, g.ID, beto, cancion("Uno", "Banda", 200,
		pool.Enlace{Plataforma: "spotify", TrackID: "s1", URL: "https://open.spotify.com/track/s1", Confianza: pool.Directo}), pool.Suelta))
	dos := cancion("Dos", "Solista", 180)
	dos.ISRC = "MX123"
	debe(t, st.AgregarCancion(ctx, g.ID, beto, dos, pool.Suelta))
	debe(t, st.AgregarCancion(ctx, g.ID, beto, cancion("Tres", "Trío", 150), pool.Suelta))
	debe(t, st.AgregarCancion(ctx, g.ID, beto, cancion("Cuatro", "Nadie", 100), pool.Suelta))
	debe(t, st.CrearEspejo(ctx, g.ID, beto, "spotify", "PLsp"))

	api := &spotifyFalso{resultados: map[string][]spotify.Track{
		"isrc:MX123": {{ID: "s2", Titulo: "Dos", Artista: "Solista"}},
		`track:"tres"`: {
			{ID: "otra", Titulo: "Tres", Artista: "Trío", DuracionS: 300}, // otra versión
			{ID: "s3", Titulo: "Tres", Artista: "Trío", DuracionS: 151},
		},
	}}

	res, err := SincronizarSpotify(ctx, st, api, g.ID, beto)
	debe(t, err)
	if res != (Resultado{Agregadas: 3, Omitidas: 1}) {
		t.Errorf("resultado = %+v", res)
	}
	if fmt.Sprint(api.reemplazos) != "[[spotify:track:s1 spotify:track:s2 spotify:track:s3]]" {
		t.Errorf("reemplazos = %v", api.reemplazos)
	}

	v, err := st.Vista(ctx, g.ID, beto)
	debe(t, err)
	if v.Pendientes != 0 || !v.Ventana[3].SinCoincidencia || !v.Ventana[0].EnPlaylist {
		t.Errorf("vista: pendientes=%d ventana=%+v", v.Pendientes, v.Ventana)
	}
	if v.Espejo.URL() != "https://open.spotify.com/playlist/PLsp" {
		t.Errorf("URL = %q", v.Espejo.URL())
	}

	// Al escuchar la primera, la playlist se reemplaza sin ella y sin volver a buscar.
	n := len(api.consultas)
	debe(t, st.MarcarEscuchada(ctx, g.ID, beto, 0))
	res, err = SincronizarSpotify(ctx, st, api, g.ID, beto)
	debe(t, err)
	if res.Quitadas != 1 || fmt.Sprint(api.reemplazos[1]) != "[spotify:track:s2 spotify:track:s3]" {
		t.Errorf("tras escuchar: res=%+v reemplazo=%v", res, api.reemplazos[1])
	}
	if len(api.consultas) != n {
		t.Errorf("no debería volver a buscar: %v", api.consultas[n:])
	}
}
