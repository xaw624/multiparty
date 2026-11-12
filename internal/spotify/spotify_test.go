package spotify

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func clientePrueba(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return NewConBase(ts.Client(), ts.URL)
}

func TestReemplazar(t *testing.T) {
	c := clientePrueba(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/playlists/PL1/items" {
			t.Errorf("petición = %s %s", r.Method, r.URL.Path)
		}
		var cuerpo struct {
			URIs []string `json:"uris"`
		}
		json.NewDecoder(r.Body).Decode(&cuerpo)
		if len(cuerpo.URIs) != 2 || cuerpo.URIs[1] != "spotify:track:b" {
			t.Errorf("uris = %v", cuerpo.URIs)
		}
		w.Write([]byte(`{"snapshot_id":"s"}`))
	})
	if err := c.Reemplazar(context.Background(), "PL1", []string{"spotify:track:a", "spotify:track:b"}); err != nil {
		t.Fatal(err)
	}
}

func TestReemplazarVacioMandaListaVacia(t *testing.T) {
	c := clientePrueba(t, func(w http.ResponseWriter, r *http.Request) {
		var cuerpo map[string]any
		json.NewDecoder(r.Body).Decode(&cuerpo)
		if uris, ok := cuerpo["uris"].([]any); !ok || len(uris) != 0 {
			t.Errorf("cuerpo = %v; quiere uris: []", cuerpo)
		}
	})
	if err := c.Reemplazar(context.Background(), "PL1", nil); err != nil {
		t.Fatal(err)
	}
}

func TestItemsDePlaylistAceptaFormatoNuevoYViejo(t *testing.T) {
	c := clientePrueba(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"next":null,"items":[
			{"item":{"id":"a","uri":"spotify:track:a","name":"Uno","type":"track","duration_ms":200400,
			  "artists":[{"name":"Banda"},{"name":"Invitada"}],"external_ids":{"isrc":"US1"},
			  "album":{"images":[{"url":"grande","width":640},{"url":"mediana","width":300},{"url":"chica","width":64}]}}},
			{"track":{"id":"b","name":"Dos","type":"track","artists":[{"name":"Otra"}]}},
			{"item":{"id":"ep","name":"Podcast","type":"episode"}},
			{"item":{"id":"loc","name":"Archivo","type":"track","is_local":true}}]}`))
	})
	ts, err := c.ItemsDePlaylist(context.Background(), "PL", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ts) != 2 {
		t.Fatalf("tracks = %+v", ts)
	}
	if ts[0] != (Track{ID: "a", URI: "spotify:track:a", Titulo: "Uno", Artista: "Banda", DuracionS: 200, ISRC: "US1", Portada: "mediana"}) {
		t.Errorf("track = %+v", ts[0])
	}
	if ts[1].URI != "spotify:track:b" {
		t.Errorf("URI por defecto = %q", ts[1].URI)
	}
}

func TestErrores(t *testing.T) {
	casos := []struct {
		estado int
		cuerpo string
		quiere error
	}{
		{401, `{"error":{"status":401,"message":"The access token expired"}}`, ErrReconectar},
		{403, `{"error":{"status":403,"message":"User not registered in the Developer Dashboard"}}`, ErrNoAutorizado},
		{429, ``, ErrLimite},
		{404, `{"error":{"status":404,"message":"Not found."}}`, ErrNoEncontrado},
	}
	for _, caso := range casos {
		c := clientePrueba(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(caso.estado)
			w.Write([]byte(caso.cuerpo))
		})
		if _, _, err := c.Yo(context.Background()); !errors.Is(err, caso.quiere) {
			t.Errorf("%d: err = %v, quiere %v", caso.estado, err, caso.quiere)
		}
	}
}

func TestBuscarLimitaA10(t *testing.T) {
	c := clientePrueba(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("limit") != "10" || r.URL.Query().Get("type") != "track" {
			t.Errorf("query = %v", r.URL.Query())
		}
		w.Write([]byte(`{"tracks":{"items":[{"id":"a","name":"Uno","type":"track","artists":[{"name":"B"}]}]}}`))
	})
	ts, err := c.Buscar(context.Background(), "isrc:US1", 50)
	if err != nil || len(ts) != 1 {
		t.Fatalf("Buscar = %+v, %v", ts, err)
	}
}

func TestEscuchadas(t *testing.T) {
	desde := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	c := clientePrueba(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("after") != "1794391200000" {
			t.Errorf("after = %q", r.URL.Query().Get("after"))
		}
		w.Write([]byte(`{"items":[{"track":{"id":"a","type":"track"},"played_at":"2026-11-11T10:05:00.000Z"}]}`))
	})
	es, err := c.Escuchadas(context.Background(), desde)
	if err != nil || len(es) != 1 || es[0].TrackID != "a" || !es[0].Cuando.Equal(desde.Add(5*time.Minute)) {
		t.Fatalf("Escuchadas = %+v, %v", es, err)
	}
}
