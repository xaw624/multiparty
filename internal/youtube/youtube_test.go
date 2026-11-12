package youtube

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func clientePrueba(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return NewConBase(ts.Client(), ts.URL)
}

func TestAgregarItem(t *testing.T) {
	c := clientePrueba(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/playlistItems" {
			t.Errorf("petición = %s %s", r.Method, r.URL.Path)
		}
		var cuerpo struct {
			Snippet struct {
				PlaylistID string `json:"playlistId"`
				ResourceID struct {
					VideoID string `json:"videoId"`
				} `json:"resourceId"`
			} `json:"snippet"`
		}
		json.NewDecoder(r.Body).Decode(&cuerpo)
		if cuerpo.Snippet.PlaylistID != "PL1" || cuerpo.Snippet.ResourceID.VideoID != "vid" {
			t.Errorf("cuerpo = %+v", cuerpo)
		}
		w.Write([]byte(`{"id":"item-1"}`))
	})
	id, err := c.AgregarItem(context.Background(), "PL1", "vid")
	if err != nil || id != "item-1" {
		t.Fatalf("AgregarItem = %q, %v", id, err)
	}
}

func TestErrores(t *testing.T) {
	casos := []struct {
		estado int
		cuerpo string
		quiere error
	}{
		{403, `{"error":{"code":403,"errors":[{"reason":"quotaExceeded"}]}}`, ErrCuota},
		{401, `{"error":{"code":401,"message":"Invalid Credentials"}}`, ErrReconectar},
		{403, `{"error":{"code":403,"errors":[{"reason":"youtubeSignupRequired"}]}}`, ErrSinCanal},
		{404, `{"error":{"code":404,"errors":[{"reason":"playlistNotFound"}]}}`, ErrNoEncontrado},
	}
	for _, caso := range casos {
		c := clientePrueba(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(caso.estado)
			w.Write([]byte(caso.cuerpo))
		})
		if _, err := c.CrearPlaylist(context.Background(), "x", "y"); !errors.Is(err, caso.quiere) {
			t.Errorf("%d %s: err = %v, quiere %v", caso.estado, caso.cuerpo, err, caso.quiere)
		}
	}
}

func TestQuitarItemYaBorradoNoEsError(t *testing.T) {
	c := clientePrueba(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		w.Write([]byte(`{"error":{"code":404,"errors":[{"reason":"playlistItemNotFound"}]}}`))
	})
	if err := c.QuitarItem(context.Background(), "x"); err != nil {
		t.Errorf("err = %v", err)
	}
}

func TestBuscarDesescapaTitulos(t *testing.T) {
	c := clientePrueba(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("videoCategoryId") != "10" {
			t.Error("la búsqueda debe limitarse a música")
		}
		w.Write([]byte(`{"items":[{"id":{"videoId":"a"},"snippet":{"title":"Guns N&#39; Roses - Sweet Child O&#39; Mine","channelTitle":"Guns N&#39; Roses"}}]}`))
	})
	v, err := c.Buscar(context.Background(), "x", 5)
	if err != nil || len(v) != 1 || v[0].Titulo != "Guns N' Roses - Sweet Child O' Mine" {
		t.Fatalf("Buscar = %+v, %v", v, err)
	}
}

func TestItemsDePlaylistPaginaYOmiteBorrados(t *testing.T) {
	c := clientePrueba(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("pageToken") == "" {
			w.Write([]byte(`{"nextPageToken":"p2","items":[
				{"snippet":{"title":"Uno","videoOwnerChannelTitle":"A - Topic","resourceId":{"videoId":"v1"}}},
				{"snippet":{"title":"Deleted video","resourceId":{"videoId":"v0"}}}]}`))
			return
		}
		w.Write([]byte(`{"items":[{"snippet":{"title":"Dos","videoOwnerChannelTitle":"B","resourceId":{"videoId":"v2"}}}]}`))
	})
	v, err := c.ItemsDePlaylist(context.Background(), "PL", 10)
	if err != nil || len(v) != 2 || v[0].ID != "v1" || v[1].ID != "v2" {
		t.Fatalf("ItemsDePlaylist = %+v, %v", v, err)
	}
}

func TestSegundosISO8601(t *testing.T) {
	casos := map[string]int{"PT3M33S": 213, "PT1H2M3S": 3723, "PT45S": 45, "PT4M": 240}
	for in, quiere := range casos {
		if got, ok := segundosISO8601(in); !ok || got != quiere {
			t.Errorf("%s = %d, %v; quiere %d", in, got, ok, quiere)
		}
	}
	for _, malo := range []string{"", "PT", "P1D", "3:33"} {
		if _, ok := segundosISO8601(malo); ok {
			t.Errorf("%q debería ser inválido", malo)
		}
	}
}
