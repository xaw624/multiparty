package tareas

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/xaw624/multiparty/internal/cifrado"
	"github.com/xaw624/multiparty/internal/conexion"
	"github.com/xaw624/multiparty/internal/db"
	"github.com/xaw624/multiparty/internal/pool"
)

type falso struct {
	mu         sync.Mutex
	escuchas   string // JSON de recently-played
	reemplazos [][]string
	busquedas  []string
	existentes string // ids de video que siguen existiendo
}

func (f *falso) servidor(t *testing.T) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /sp/me/player/recently-played", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, f.escuchas)
	})
	mux.HandleFunc("PUT /sp/playlists/PLsp/items", func(w http.ResponseWriter, r *http.Request) {
		var c struct {
			URIs []string `json:"uris"`
		}
		json.NewDecoder(r.Body).Decode(&c)
		f.mu.Lock()
		f.reemplazos = append(f.reemplazos, c.URIs)
		f.mu.Unlock()
	})
	mux.HandleFunc("GET /sp/search", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.busquedas = append(f.busquedas, r.URL.Query().Get("q"))
		f.mu.Unlock()
		fmt.Fprint(w, `{"tracks":{"items":[{"id":"sp-nueva","name":"Nueva","type":"track","artists":[{"name":"Artista"}]}]}}`)
	})
	mux.HandleFunc("GET /yt/videos", func(w http.ResponseWriter, r *http.Request) {
		var items []string
		for _, id := range strings.Split(r.URL.Query().Get("id"), ",") {
			if strings.Contains(f.existentes, id) {
				items = append(items, fmt.Sprintf(`{"id":%q,"contentDetails":{"duration":"PT3M"}}`, id))
			}
		}
		fmt.Fprintf(w, `{"items":[%s]}`, strings.Join(items, ","))
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func preparar(t *testing.T) (*Tareas, *pool.Store, *falso, int64, int64, func(string, ...any)) {
	t.Helper()
	ctx := context.Background()
	database, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if err := db.Migrate(ctx, database); err != nil {
		t.Fatal(err)
	}
	st := pool.NewStore(database)
	f := &falso{}
	ts := f.servidor(t)
	caja, _ := cifrado.New(bytes.Repeat([]byte{3}, cifrado.TamClave))
	conf := &oauth2.Config{Endpoint: oauth2.Endpoint{TokenURL: ts.URL + "/token"}}
	con, err := conexion.New(st, slog.New(slog.NewTextHandler(io.Discard, nil)), conexion.Opciones{
		Google: conf, Spotify: conf, Caja: caja,
		Bases: map[string]string{conexion.Spotify: ts.URL + "/sp", conexion.YouTube: ts.URL + "/yt"},
	})
	if err != nil {
		t.Fatal(err)
	}

	beto, _ := st.CrearUsuario(ctx, "Beto")
	g, _ := st.CrearGrupo(ctx, "Amigos", beto)
	for _, p := range []string{"spotify", "youtube"} {
		if err := st.GuardarCuenta(ctx, pool.Cuenta{
			UsuarioID: beto, Plataforma: p, IDExterno: "id-" + p,
			AccessCifrado: caja.Sellar([]byte("token")), RefreshCifrado: caja.Sellar([]byte("refresh")),
			Expira: time.Now().Add(time.Hour),
		}); err != nil {
			t.Fatal(err)
		}
	}
	exec := func(q string, args ...any) {
		if _, err := database.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	return New(st, con, slog.New(slog.NewTextHandler(io.Discard, nil))), st, f, g.ID, beto, exec
}

func cancion(titulo, spotifyID string) pool.CancionResuelta {
	c := pool.CancionResuelta{Titulo: titulo, Artista: "Artista",
		Enlaces: []pool.Enlace{{Plataforma: "deezer", TrackID: "dz-" + titulo, URL: "https://deezer.test/" + titulo}}}
	if spotifyID != "" {
		c.Enlaces = append(c.Enlaces, pool.Enlace{Plataforma: "spotify", TrackID: spotifyID,
			URL: "https://open.spotify.com/track/" + spotifyID, Confianza: pool.Directo})
	}
	return c
}

func TestEscuchasSpotifyAvanzanLaVentana(t *testing.T) {
	ctx := context.Background()
	tr, st, f, g, beto, _ := preparar(t)
	for _, c := range []pool.CancionResuelta{cancion("Uno", "s1"), cancion("Dos", "s2")} {
		if err := st.AgregarCancion(ctx, g, beto, c, pool.Suelta); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.CrearEspejo(ctx, g, beto, "spotify", "PLsp"); err != nil {
		t.Fatal(err)
	}

	// Primera corrida: solo fija el punto de partida.
	t0 := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	tr.ahora = func() time.Time { return t0 }
	f.escuchas = `{"items":[{"track":{"id":"s1"},"played_at":"2026-11-11T11:00:00Z"}]}`
	if err := tr.EscuchasSpotify(ctx); err != nil {
		t.Fatal(err)
	}
	if v, _ := st.Vista(ctx, g, beto); v.Yo.Cursor != 0 {
		t.Fatal("la primera corrida no debe marcar escuchas anteriores")
	}

	// Escuchó «Uno» y otra canción que no está en su ventana.
	f.escuchas = `{"items":[
		{"track":{"id":"otra"},"played_at":"2026-11-11T12:04:00Z"},
		{"track":{"id":"s1"},"played_at":"2026-11-11T12:03:00Z"}]}`
	if err := tr.EscuchasSpotify(ctx); err != nil {
		t.Fatal(err)
	}
	v, _ := st.Vista(ctx, g, beto)
	if v.Yo.Cursor != 1 || len(v.Ventana) != 1 || v.Ventana[0].Titulo != "Dos" {
		t.Errorf("cursor=%d ventana=%+v", v.Yo.Cursor, v.Ventana)
	}
	if fmt.Sprint(f.reemplazos) != "[[spotify:track:s2]]" {
		t.Errorf("reemplazos = %v", f.reemplazos)
	}
	desde, _, _ := st.CursorEscuchas(ctx, beto, "spotify")
	if !desde.Equal(time.Date(2026, 11, 11, 12, 4, 0, 0, time.UTC)) {
		t.Errorf("cursor de escuchas = %v", desde)
	}
}

func TestPreemparejarBuscaLasProximas(t *testing.T) {
	ctx := context.Background()
	tr, st, f, g, beto, _ := preparar(t)
	if err := st.AgregarCancion(ctx, g, beto, cancion("Nueva", ""), pool.Suelta); err != nil {
		t.Fatal(err)
	}
	if err := st.CrearEspejo(ctx, g, beto, "spotify", "PLsp"); err != nil {
		t.Fatal(err)
	}
	if err := tr.Preemparejar(ctx); err != nil {
		t.Fatal(err)
	}
	plan, _ := st.PlanDeEspejo(ctx, g, beto)
	if len(plan.Ventana) != 1 || plan.Ventana[0].TrackID != "sp-nueva" {
		t.Errorf("ventana = %+v", plan.Ventana)
	}
	// La siguiente corrida no vuelve a buscar.
	n := len(f.busquedas)
	tr.Preemparejar(ctx)
	if len(f.busquedas) != n {
		t.Error("no debería volver a buscar una canción ya emparejada")
	}
}

func TestRefrescarYouTube(t *testing.T) {
	ctx := context.Background()
	tr, st, f, g, beto, exec := preparar(t)
	vivo := cancion("Viva", "")
	vivo.Enlaces = append(vivo.Enlaces, pool.Enlace{Plataforma: "youtubeMusic", TrackID: "vivo", URL: "https://music.youtube.com/watch?v=vivo"})
	borrado := cancion("Borrada", "")
	borrado.Enlaces = append(borrado.Enlaces, pool.Enlace{Plataforma: "youtubeMusic", TrackID: "borrado", URL: "https://music.youtube.com/watch?v=borrado"})
	st.AgregarCancion(ctx, g, beto, vivo, pool.Suelta)
	st.AgregarCancion(ctx, g, beto, borrado, pool.Suelta)
	exec(`UPDATE mapeos SET verificado_en = '2026-01-01 00:00:00' WHERE plataforma = 'youtubeMusic'`)
	f.existentes = "vivo"

	if err := tr.RefrescarYouTube(ctx); err != nil {
		t.Fatal(err)
	}
	quedan, err := st.MapeosPorVerificar(ctx, time.Now().Add(time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(quedan) != 1 || quedan[0].TrackID != "vivo" {
		t.Errorf("mapeos = %+v (el borrado debería desaparecer)", quedan)
	}
	if viejos, _ := st.MapeosPorVerificar(ctx, time.Now().Add(-24*time.Hour), 10); len(viejos) != 0 {
		t.Errorf("el vivo debería quedar verificado hoy: %+v", viejos)
	}
}
