package resolver

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/xaw624/multiparty/internal/pool"
)

func TestDetectar(t *testing.T) {
	casos := []struct {
		enlace     string
		plataforma string
		id         string
		err        error
	}{
		{"https://open.spotify.com/track/4PTG3Z6ehGkBFwjybzWkR8?si=abc", "spotify", "4PTG3Z6ehGkBFwjybzWkR8", nil},
		{"https://open.spotify.com/intl-es/track/4PTG3Z6ehGkBFwjybzWkR8", "spotify", "4PTG3Z6ehGkBFwjybzWkR8", nil},
		{"https://open.spotify.com/album/6N9PS4QXF1D0OWPk0Sxtb4", "", "", pool.ErrNoEsCancion},
		{"https://open.spotify.com/playlist/37i9dQZF1DXcBWIGoYBM5M", "", "", pool.ErrNoEsCancion},
		{"https://www.youtube.com/watch?v=dQw4w9WgXcQ&t=10", "youtube", "dQw4w9WgXcQ", nil},
		{"https://music.youtube.com/watch?v=lYBUbBu4W08&list=RDAMVM", "youtube", "lYBUbBu4W08", nil},
		{"https://m.youtube.com/watch?v=dQw4w9WgXcQ", "youtube", "dQw4w9WgXcQ", nil},
		{"https://youtu.be/dQw4w9WgXcQ?si=x", "youtube", "dQw4w9WgXcQ", nil},
		{"https://www.youtube.com/shorts/abcDEF12345", "youtube", "abcDEF12345", nil},
		{"https://music.youtube.com/playlist?list=PL123", "", "", pool.ErrNoEsCancion},
		{"https://music.apple.com/mx/album/never-gonna/1624945511?i=1624945512", "apple", "1624945512", nil},
		{"https://music.apple.com/us/song/never-gonna/1624945512", "apple", "1624945512", nil},
		{"https://music.apple.com/mx/album/whenever/1624945511", "", "", pool.ErrNoEsCancion},
		{"https://www.deezer.com/mx/track/14408104", "deezer", "14408104", nil},
		{"https://www.deezer.com/track/14408104", "deezer", "14408104", nil},
		{"https://www.deezer.com/mx/album/1234", "", "", pool.ErrNoEsCancion},
		{"https://tidal.com/browse/track/1234", "", "", pool.ErrNoSoportada},
		{"https://open.spotify.com/track/../../etc", "", "", pool.ErrEnlaceInvalido},
	}
	for _, c := range casos {
		t.Run(c.enlace, func(t *testing.T) {
			u, _ := url.Parse(c.enlace)
			o, err := detectar(u)
			if !errors.Is(err, c.err) {
				t.Fatalf("err = %v, quiere %v", err, c.err)
			}
			if c.err == nil && (o.plataforma != c.plataforma || o.id != c.id) {
				t.Errorf("= %+v, quiere %s/%s", o, c.plataforma, c.id)
			}
		})
	}
}

// Respuestas recortadas con la forma de las reales (comprobadas 2026-11-11).
const (
	paginaSpotify = `<html><head>
<meta property="og:title" content="Never Gonna Give You Up"/>
<meta property="og:description" content="Rick Astley · Whenever You Need Somebody · Song · 1987"/>
<meta property="og:image" content="https://i.scdn.co/image/portada"/>
<meta name="music:duration" content="214"/>
<meta name="music:musician_description" content="Rick Astley"/>
</head></html>`

	deezerBusqueda = `{"data":[
  {"id":1,"title":"Never Gonna Stop","duration":200,"isrc":"XX0000000001","link":"https://www.deezer.com/track/1","artist":{"name":"Otro"},"album":{"cover_medium":"https://dz/1.jpg"}},
  {"id":14408104,"title":"Never Gonna Give You Up","duration":211,"isrc":"GBARL0600786","link":"https://www.deezer.com/track/14408104","artist":{"name":"Rick Astley"},"album":{"cover_medium":"https://dz/c.jpg"}}
],"total":2}`

	deezerError = `{"error":{"type":"DataException","message":"no data","code":800}}`

	itunesBusqueda = `{"resultCount":2,"results":[
  {"kind":"music-video","trackId":9,"trackName":"Never Gonna Give You Up","artistName":"Rick Astley"},
  {"kind":"song","trackId":1624945512,"trackName":"Never Gonna Give You Up (2022 Remaster)","artistName":"Rick Astley","trackViewUrl":"https://music.apple.com/mx/album/x/1624945511?i=1624945512&uo=4","artworkUrl100":"https://a/100x100bb.jpg","trackTimeMillis":213560}
]}`

	youtubeOEmbed = `{"title":"Rick Astley - Never Gonna Give You Up (Official Video) (4K Remaster)","author_name":"Rick Astley","thumbnail_url":"https://i.ytimg.com/vi/dQw4w9WgXcQ/hqdefault.jpg"}`
)

// fuentesFalsas sirve las cuatro fuentes. Si deezerFalla, Deezer responde 500.
func fuentesFalsas(t *testing.T, deezerFalla bool) *Resolver {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/sp/track/4PTG3Z6ehGkBFwjybzWkR8", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(paginaSpotify))
	})
	mux.HandleFunc("/sp/track/", http.NotFound)
	mux.HandleFunc("/deezer/search", func(w http.ResponseWriter, r *http.Request) {
		if deezerFalla {
			http.Error(w, "caído", http.StatusInternalServerError)
			return
		}
		if !strings.Contains(r.URL.Query().Get("q"), "Rick Astley") {
			t.Errorf("búsqueda en Deezer sin artista: %q", r.URL.Query().Get("q"))
		}
		w.Write([]byte(deezerBusqueda))
	})
	mux.HandleFunc("/deezer/track/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(deezerError))
	})
	mux.HandleFunc("/itunes/search", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(itunesBusqueda))
	})
	mux.HandleFunc("/yt", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(youtubeOEmbed))
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	r := New("MX")
	r.base = bases{deezer: ts.URL + "/deezer", itunes: ts.URL + "/itunes", youtube: ts.URL + "/yt", spotify: ts.URL + "/sp"}
	return r
}

func enlacesPorPlataforma(c pool.CancionResuelta) map[string]pool.Enlace {
	m := map[string]pool.Enlace{}
	for _, e := range c.Enlaces {
		m[e.Plataforma] = e
	}
	return m
}

func TestResolverSpotify(t *testing.T) {
	r := fuentesFalsas(t, false)
	c, err := r.Resolver(context.Background(), "https://open.spotify.com/track/4PTG3Z6ehGkBFwjybzWkR8?si=xyz")
	if err != nil {
		t.Fatal(err)
	}
	if c.Titulo != "Never Gonna Give You Up" || c.Artista != "Rick Astley" || c.ISRC != "GBARL0600786" {
		t.Errorf("metadatos = %+v", c)
	}
	e := enlacesPorPlataforma(c)
	if e["spotify"].Confianza != pool.Directo || e["spotify"].URL != "https://open.spotify.com/track/4PTG3Z6ehGkBFwjybzWkR8" {
		t.Errorf("spotify = %+v", e["spotify"])
	}
	if e["deezer"].TrackID != "14408104" || e["deezer"].Confianza != pool.Busqueda {
		t.Errorf("deezer = %+v", e["deezer"])
	}
	if e["appleMusic"].URL != "https://music.apple.com/mx/album/x/1624945511?i=1624945512" {
		t.Errorf("apple = %+v", e["appleMusic"])
	}
	if e["songlink"].URL != "https://song.link/https://open.spotify.com/track/4PTG3Z6ehGkBFwjybzWkR8" {
		t.Errorf("songlink = %+v", e["songlink"])
	}
}

func TestResolverYouTubeUsaDatosLimpios(t *testing.T) {
	r := fuentesFalsas(t, false)
	c, err := r.Resolver(context.Background(), "https://youtu.be/dQw4w9WgXcQ")
	if err != nil {
		t.Fatal(err)
	}
	if c.Titulo != "Never Gonna Give You Up" || c.Artista != "Rick Astley" || c.PortadaURL != "https://dz/c.jpg" {
		t.Errorf("metadatos = %+v", c)
	}
	e := enlacesPorPlataforma(c)
	if e["youtubeMusic"].URL != "https://music.youtube.com/watch?v=dQw4w9WgXcQ" || e["youtubeMusic"].TrackID != "dQw4w9WgXcQ" {
		t.Errorf("youtubeMusic = %+v", e["youtubeMusic"])
	}
}

func TestResolverSigueSiFallaUnaFuenteSecundaria(t *testing.T) {
	r := fuentesFalsas(t, true)
	c, err := r.Resolver(context.Background(), "https://open.spotify.com/track/4PTG3Z6ehGkBFwjybzWkR8")
	if err != nil {
		t.Fatalf("no debería fallar: %v", err)
	}
	e := enlacesPorPlataforma(c)
	if _, ok := e["deezer"]; ok {
		t.Error("no debería haber enlace de Deezer")
	}
	if _, ok := e["songlink"]; !ok {
		t.Error("falta el enlace de song.link")
	}
}

func TestResolverErrores(t *testing.T) {
	r := fuentesFalsas(t, false)
	casos := map[string]error{
		"https://open.spotify.com/track/NoExiste123":       pool.ErrNoEncontrada,
		"https://www.deezer.com/track/999":                 pool.ErrNoEncontrada,
		"https://open.spotify.com/album/6N9PS4QXF1D0OWPk0": pool.ErrNoEsCancion,
		"https://soundcloud.com/artista/cancion":           pool.ErrNoSoportada,
		"no es un link":                                    pool.ErrEnlaceInvalido,
		"javascript:alert(1)":                              pool.ErrEnlaceInvalido,
	}
	for enlace, quiere := range casos {
		if _, err := r.Resolver(context.Background(), enlace); !errors.Is(err, quiere) {
			t.Errorf("%s: err = %v, quiere %v", enlace, err, quiere)
		}
	}
}
