package web

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/oauth2"

	"github.com/xaw624/multiparty/internal/cifrado"
	"github.com/xaw624/multiparty/internal/db"
	"github.com/xaw624/multiparty/internal/pool"
	"github.com/xaw624/multiparty/internal/spotify"
	"github.com/xaw624/multiparty/internal/youtube"
)

// googleFalso simula el servidor OAuth de Google y la YouTube Data API.
type googleFalso struct {
	mu           sync.Mutex
	agregados    []string
	quitados     []string
	revocados    []string
	reemplazos   [][]string
	tokensUsados map[string]bool
}

func (g *googleFalso) handler(t *testing.T) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		w.Header().Set("Content-Type", "application/json")
		switch r.PostForm.Get("grant_type") {
		case "authorization_code":
			if r.PostForm.Get("code_verifier") == "" {
				t.Error("falta code_verifier (PKCE)")
			}
			// expires_in corto: el primer uso obliga a renovar.
			fmt.Fprintf(w, `{"access_token":"at1","refresh_token":"rt1","expires_in":5,"token_type":"Bearer","scope":%q}`,
				youtube.Scope+" "+strings.Join(spotify.Scopes, " "))
		case "refresh_token":
			if r.PostForm.Get("refresh_token") != "rt1" {
				w.WriteHeader(400)
				fmt.Fprint(w, `{"error":"invalid_grant"}`)
				return
			}
			fmt.Fprint(w, `{"access_token":"at2","expires_in":3600,"token_type":"Bearer"}`)
		}
	})
	mux.HandleFunc("POST /revoke", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		g.mu.Lock()
		g.revocados = append(g.revocados, r.PostForm.Get("token"))
		g.mu.Unlock()
	})
	yt := http.NewServeMux()
	yt.HandleFunc("GET /channels", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"items":[{"id":"UC1","snippet":{"title":"Canal de Ana"}}]}`)
	})
	yt.HandleFunc("POST /playlists", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"id":"PL1"}`)
	})
	yt.HandleFunc("POST /playlistItems", func(w http.ResponseWriter, r *http.Request) {
		var c struct {
			Snippet struct {
				ResourceID struct {
					VideoID string `json:"videoId"`
				} `json:"resourceId"`
			} `json:"snippet"`
		}
		json.NewDecoder(r.Body).Decode(&c)
		g.mu.Lock()
		g.agregados = append(g.agregados, c.Snippet.ResourceID.VideoID)
		g.mu.Unlock()
		fmt.Fprintf(w, `{"id":"item-%s"}`, c.Snippet.ResourceID.VideoID)
	})
	yt.HandleFunc("DELETE /playlistItems", func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		g.quitados = append(g.quitados, r.URL.Query().Get("id"))
		g.mu.Unlock()
		w.WriteHeader(204)
	})
	yt.HandleFunc("GET /playlists", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"items":[{"id":"PLmio","snippet":{"title":"Favoritas"},"contentDetails":{"itemCount":3}}]}`)
	})
	yt.HandleFunc("GET /playlistItems", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"items":[
			{"snippet":{"title":"Importada Uno","videoOwnerChannelTitle":"Artista - Topic","resourceId":{"videoId":"y1"}}},
			{"snippet":{"title":"Canción Uno","videoOwnerChannelTitle":"Artista - Topic","resourceId":{"videoId":"y2"}}},
			{"snippet":{"title":"Rara","videoOwnerChannelTitle":"Nadie","resourceId":{"videoId":"y3"}}}]}`)
	})
	yt.HandleFunc("GET /search", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"items":[{"id":{"videoId":"v1"},"snippet":{"title":"Canción Uno","channelTitle":"Artista - Topic"}}]}`)
	})
	sp := http.NewServeMux()
	sp.HandleFunc("GET /me", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"id":"beto123","display_name":"Beto"}`)
	})
	sp.HandleFunc("POST /me/playlists", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(201)
		fmt.Fprint(w, `{"id":"PLsp"}`)
	})
	sp.HandleFunc("GET /me/playlists", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"next":null,"items":[{"id":"PLmio","name":"Mías","items":{"total":1}}]}`)
	})
	sp.HandleFunc("GET /playlists/PLmio/items", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"next":null,"items":[{"item":{"id":"trackA1","name":"De Spotify","type":"track",
			"duration_ms":180000,"artists":[{"name":"Banda"}],"external_ids":{"isrc":"US9"}}}]}`)
	})
	sp.HandleFunc("PUT /playlists/PLsp/items", func(w http.ResponseWriter, r *http.Request) {
		var c struct {
			URIs []string `json:"uris"`
		}
		json.NewDecoder(r.Body).Decode(&c)
		g.mu.Lock()
		g.reemplazos = append(g.reemplazos, c.URIs)
		g.mu.Unlock()
		fmt.Fprint(w, `{"snapshot_id":"x"}`)
	})
	mux.Handle("/sp/", http.StripPrefix("/sp", sp))
	mux.Handle("/yt/", http.StripPrefix("/yt", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		g.tokensUsados[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")] = true
		g.mu.Unlock()
		yt.ServeHTTP(w, r)
	})))
	return mux
}

type entornoYouTube struct {
	app    *httptest.Server
	google *googleFalso
	db     *sql.DB
	caja   *cifrado.Caja
}

func nuevoEntornoYouTube(t *testing.T) entornoYouTube {
	t.Helper()
	g := &googleFalso{tokensUsados: map[string]bool{}}
	gs := httptest.NewServer(g.handler(t))
	t.Cleanup(gs.Close)

	database, err := db.Open(filepath.Join(t.TempDir(), "yt.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if err := db.Migrate(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	caja, _ := cifrado.New(bytes.Repeat([]byte{1}, cifrado.TamClave))
	res := resolvedorFalso{
		"https://open.spotify.com/track/1":     cancionFalsa("Canción Uno"),
		"https://music.youtube.com/watch?v=y1": cancionFalsa("Importada Uno"),
		"https://music.youtube.com/watch?v=y2": cancionFalsa("Canción Uno"),
		"https://www.deezer.com/track/77": {Titulo: "Rara", Artista: "Nadie",
			Enlaces: []pool.Enlace{{Plataforma: "deezer", TrackID: "77", URL: "https://www.deezer.com/track/77"}}},
	}
	srv, err := New(database, slog.New(slog.NewTextHandler(io.Discard, nil)), Opciones{
		Resolver: res,
		Secreto:  []byte("clave-de-prueba"),
		Caja:     caja,
		Google: &oauth2.Config{
			ClientID: "cliente", ClientSecret: "secreto",
			Endpoint:    oauth2.Endpoint{AuthURL: gs.URL + "/auth", TokenURL: gs.URL + "/token"},
			RedirectURL: "http://app.test/cuentas/youtube/callback",
			Scopes:      []string{youtube.Scope},
		},
		Spotify: &oauth2.Config{
			ClientID: "cliente-sp", ClientSecret: "secreto",
			Endpoint:    oauth2.Endpoint{AuthURL: gs.URL + "/auth-sp", TokenURL: gs.URL + "/token"},
			RedirectURL: "http://app.test/cuentas/spotify/callback",
			Scopes:      spotify.Scopes,
		},
		YouTubeBase: gs.URL + "/yt",
		SpotifyBase: gs.URL + "/sp",
		RevocarURL:  gs.URL + "/revoke",
	})
	if err != nil {
		t.Fatal(err)
	}
	app := httptest.NewServer(srv.Routes())
	t.Cleanup(app.Close)
	return entornoYouTube{app: app, google: g, db: database, caja: caja}
}

func get(t *testing.T, c *http.Client, u string) (*http.Response, string) {
	t.Helper()
	resp, err := c.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func TestFlujoYouTube(t *testing.T) {
	e := nuevoEntornoYouTube(t)
	ana := cliente(t)

	resp, _ := post(t, ana, e.app.URL+"/grupos", url.Values{"nombre_grupo": {"Amigos"}, "tu_nombre": {"Ana"}}, false)
	rutaG := resp.Request.URL.Path
	urlGrupo := e.app.URL + rutaG
	post(t, ana, urlGrupo+"/aportes", url.Values{"enlace": {"https://open.spotify.com/track/1"}}, true)

	// 1. Conectar: redirige a Google con PKCE, acceso offline y consentimiento.
	sinSeguir := *ana
	sinSeguir.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, _ = get(t, &sinSeguir, e.app.URL+"/cuentas/youtube/conectar?volver="+rutaG)
	destino, _ := url.Parse(resp.Header.Get("Location"))
	q := destino.Query()
	if resp.StatusCode != http.StatusFound || q.Get("code_challenge_method") != "S256" ||
		q.Get("access_type") != "offline" || q.Get("prompt") != "consent" || q.Get("state") == "" {
		t.Fatalf("redirección a Google: %d %s", resp.StatusCode, destino)
	}

	// Un state distinto se rechaza.
	resp, _ = get(t, ana, e.app.URL+"/cuentas/youtube/callback?state=otro&code=x")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("state inválido: %d", resp.StatusCode)
	}

	// 2. Volver de Google con el código.
	resp, _ = get(t, &sinSeguir, e.app.URL+"/cuentas/youtube/conectar?volver="+rutaG)
	destino, _ = url.Parse(resp.Header.Get("Location"))
	_, cuerpo := get(t, ana, e.app.URL+"/cuentas/youtube/callback?code=abc&state="+destino.Query().Get("state"))
	debeContener(t, cuerpo, "YouTube conectado", "Conectado como <strong>Canal de Ana</strong>", "Crear mi playlist en YouTube Music")

	// Los tokens se guardan cifrados.
	var refresh []byte
	e.db.QueryRow(`SELECT refresh_token_cifrado FROM cuentas`).Scan(&refresh)
	if bytes.Contains(refresh, []byte("rt1")) {
		t.Error("el refresh token se guardó sin cifrar")
	}

	// 3. Crear la playlist: se empareja la canción y se agrega.
	_, cuerpo = post(t, ana, urlGrupo+"/espejo/youtube", nil, true)
	debeContener(t, cuerpo, "Playlist actualizada: 1 agregada", "Abrir mi playlist", "list=PL1", "Tu playlist está al día")
	if fmt.Sprint(e.google.agregados) != "[v1]" {
		t.Errorf("agregados = %v", e.google.agregados)
	}
	// El token caducado se renovó y se guardó.
	var access []byte
	e.db.QueryRow(`SELECT access_token_cifrado FROM cuentas`).Scan(&access)
	if a, err := e.caja.Abrir(access); err != nil || string(a) != "at2" {
		t.Errorf("access token guardado = %q, %v", a, err)
	}
	if !e.google.tokensUsados["at2"] {
		t.Error("las llamadas a YouTube deberían usar el token renovado")
	}

	// 4. «Ya la escuché» también la quita de la playlist.
	post(t, ana, urlGrupo+"/escuchada/0", nil, true)
	if fmt.Sprint(e.google.quitados) != "[item-v1]" {
		t.Errorf("quitados = %v", e.google.quitados)
	}

	// 5. Desconectar revoca el token y vuelve a ofrecer conectar.
	_, cuerpo = post(t, ana, e.app.URL+"/cuentas/youtube/desconectar", url.Values{"volver": {rutaG}}, false)
	debeContener(t, cuerpo, "YouTube desconectado", "Conectar YouTube")
	if fmt.Sprint(e.google.revocados) != "[rt1]" {
		t.Errorf("revocados = %v", e.google.revocados)
	}
}

func TestVolverNoPermiteRedireccionesAbiertas(t *testing.T) {
	e := nuevoEntornoYouTube(t)
	ana := cliente(t)
	post(t, ana, e.app.URL+"/grupos", url.Values{"nombre_grupo": {"Amigos"}, "tu_nombre": {"Ana"}}, false)

	sinSeguir := *ana
	sinSeguir.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, _ := get(t, &sinSeguir, e.app.URL+"/cuentas/youtube/conectar?volver=https://malo.test")
	destino, _ := url.Parse(resp.Header.Get("Location"))
	resp, _ = get(t, &sinSeguir, e.app.URL+"/cuentas/youtube/callback?error=access_denied&state="+destino.Query().Get("state"))
	if loc := resp.Header.Get("Location"); !strings.HasPrefix(loc, "/?") {
		t.Errorf("redirección = %q, debería quedarse en el sitio", loc)
	}
}

func TestSinGoogleNoHayRutasDeYouTube(t *testing.T) {
	ts := servidorPrueba(t)
	ana := cliente(t)
	_, cuerpo := post(t, ana, ts.URL+"/grupos", url.Values{"nombre_grupo": {"Amigos"}, "tu_nombre": {"Ana"}}, false)
	if strings.Contains(cuerpo, "Conectar YouTube") || strings.Contains(cuerpo, "Escuchar en tu app") {
		t.Error("sin Google configurado no debe mostrarse la sección de YouTube")
	}
	resp, _ := get(t, ana, ts.URL+"/cuentas/youtube/conectar")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("conectar sin Google: %d", resp.StatusCode)
	}
}

func TestImportarDeYouTube(t *testing.T) {
	e := nuevoEntornoYouTube(t)
	ana := cliente(t)
	resp, _ := post(t, ana, e.app.URL+"/grupos", url.Values{"nombre_grupo": {"Amigos"}, "tu_nombre": {"Ana"}}, false)
	rutaG := resp.Request.URL.Path
	urlGrupo := e.app.URL + rutaG
	post(t, ana, urlGrupo+"/aportes", url.Values{"enlace": {"https://open.spotify.com/track/1"}}, true)

	// Sin conectar YouTube, la página lo pide.
	_, cuerpo := get(t, ana, urlGrupo+"/importar")
	debeContener(t, cuerpo, "Primero conecta tu cuenta de YouTube")

	sinSeguir := *ana
	sinSeguir.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, _ = get(t, &sinSeguir, e.app.URL+"/cuentas/youtube/conectar?volver="+rutaG)
	destino, _ := url.Parse(resp.Header.Get("Location"))
	get(t, ana, e.app.URL+"/cuentas/youtube/callback?code=abc&state="+destino.Query().Get("state"))

	_, cuerpo = get(t, ana, urlGrupo+"/importar")
	debeContener(t, cuerpo, "Favoritas", "playlist=PLmio")
	_, cuerpo = get(t, ana, urlGrupo+"/importar?playlist=PLmio&t=Favoritas")
	debeContener(t, cuerpo, "Importada Uno", `value="y1"`, `value="y3"`, "de «Favoritas»")

	// y1 entra, y2 ya estaba (misma canción) e y3 no se encuentra.
	_, cuerpo = post(t, ana, urlGrupo+"/importar", url.Values{"de": {"youtube"}, "v": {"y1", "y2", "y3", "../malo"}}, false)
	debeContener(t, cuerpo, "Importación: 1 canción agregada, 1 ya estaba, 1 no se encontró.", "Importada Uno")
}

func TestFlujoSpotify(t *testing.T) {
	e := nuevoEntornoYouTube(t)
	beto := cliente(t)
	resp, _ := post(t, beto, e.app.URL+"/grupos", url.Values{"nombre_grupo": {"Amigos"}, "tu_nombre": {"Beto"}}, false)
	rutaG := resp.Request.URL.Path
	urlGrupo := e.app.URL + rutaG
	post(t, beto, urlGrupo+"/aportes", url.Values{"enlace": {"https://open.spotify.com/track/1"}}, true)

	sinSeguir := *beto
	sinSeguir.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, _ = get(t, &sinSeguir, e.app.URL+"/cuentas/spotify/conectar?volver="+rutaG)
	destino, _ := url.Parse(resp.Header.Get("Location"))
	if destino.Path != "/auth-sp" || destino.Query().Get("show_dialog") != "true" || destino.Query().Get("code_challenge") == "" {
		t.Fatalf("redirección a Spotify: %s", destino)
	}
	// El state de Spotify no sirve para el callback de YouTube.
	resp, _ = get(t, beto, e.app.URL+"/cuentas/youtube/callback?code=abc&state="+destino.Query().Get("state"))
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("callback cruzado: %d", resp.StatusCode)
	}
	resp, _ = get(t, &sinSeguir, e.app.URL+"/cuentas/spotify/conectar?volver="+rutaG)
	destino, _ = url.Parse(resp.Header.Get("Location"))
	_, cuerpo := get(t, beto, e.app.URL+"/cuentas/spotify/callback?code=abc&state="+destino.Query().Get("state"))
	debeContener(t, cuerpo, "Spotify conectado", "Conectado como <strong>Beto</strong>", "Crear mi playlist en Spotify")

	_, cuerpo = post(t, beto, urlGrupo+"/espejo/spotify", nil, true)
	debeContener(t, cuerpo, "Playlist actualizada: 1 agregada", "open.spotify.com/playlist/PLsp", "Se actualiza sola")
	if fmt.Sprint(e.google.reemplazos) != "[[spotify:track:sp-Canción Uno]]" {
		t.Errorf("reemplazos = %v", e.google.reemplazos)
	}

	// Importar de Spotify usa los datos de la API (con ISRC).
	_, cuerpo = get(t, beto, urlGrupo+"/importar?de=spotify")
	debeContener(t, cuerpo, "Importar de Spotify", "Mías")
	_, cuerpo = get(t, beto, urlGrupo+"/importar?de=spotify&playlist=PLmio&t=M%C3%ADas")
	debeContener(t, cuerpo, "De Spotify", `name="playlist" value="PLmio"`)
	_, cuerpo = post(t, beto, urlGrupo+"/importar", url.Values{"de": {"spotify"}, "playlist": {"PLmio"}, "v": {"trackA1", "noexiste"}}, false)
	debeContener(t, cuerpo, "Importación: 1 canción agregada, 1 no se encontró.", "De Spotify")
	var isrc string
	e.db.QueryRow(`SELECT isrc FROM canciones WHERE titulo = 'De Spotify'`).Scan(&isrc)
	if isrc != "US9" {
		t.Errorf("isrc = %q", isrc)
	}
}

func TestRevisionManual(t *testing.T) {
	e := nuevoEntornoYouTube(t)
	ana := cliente(t)
	resp, _ := post(t, ana, e.app.URL+"/grupos", url.Values{"nombre_grupo": {"Amigos"}, "tu_nombre": {"Ana"}}, false)
	rutaG := resp.Request.URL.Path
	urlGrupo := e.app.URL + rutaG
	post(t, ana, urlGrupo+"/aportes", url.Values{"enlace": {"https://www.deezer.com/track/77"}}, true)

	sinSeguir := *ana
	sinSeguir.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, _ = get(t, &sinSeguir, e.app.URL+"/cuentas/youtube/conectar?volver="+rutaG)
	destino, _ := url.Parse(resp.Header.Get("Location"))
	get(t, ana, e.app.URL+"/cuentas/youtube/callback?code=abc&state="+destino.Query().Get("state"))

	// La búsqueda en YouTube no encuentra «Rara»: queda en revisión.
	_, cuerpo := post(t, ana, urlGrupo+"/espejo/youtube", nil, true)
	debeContener(t, cuerpo, "1 canción no está en YouTube", "1 canción por revisar", "No está en YouTube")

	_, cuerpo = get(t, ana, urlGrupo+"/revision")
	debeContener(t, cuerpo, "Rara", "Canción Uno", `value="v1"`, "Volver a buscar")

	// Un link de otra plataforma no sirve.
	_, cuerpo = post(t, ana, urlGrupo+"/revision/1", url.Values{"accion": {"link"}, "enlace": {"https://open.spotify.com/track/abc123"}}, false)
	debeContener(t, cuerpo, "Pega un link de una canción de YouTube")
	// Un id que no está entre los candidatos tampoco.
	_, cuerpo = post(t, ana, urlGrupo+"/revision/1", url.Values{"accion": {"elegir"}, "track": {"inventado"}}, false)
	debeContener(t, cuerpo, "Elige una opción")

	_, cuerpo = post(t, ana, urlGrupo+"/revision/1", url.Values{"accion": {"link"}, "enlace": {"https://youtu.be/correcto1"}}, false)
	debeContener(t, cuerpo, "Listo: «Rara»", "No hay nada por revisar")

	// La siguiente sincronización la agrega con el id elegido a mano.
	_, cuerpo = post(t, ana, urlGrupo+"/sincronizar", nil, true)
	debeContener(t, cuerpo, "Playlist actualizada: 1 agregada")
	if fmt.Sprint(e.google.agregados) != "[correcto1]" {
		t.Errorf("agregados = %v", e.google.agregados)
	}
	var conf string
	e.db.QueryRow(`SELECT confianza FROM mapeos WHERE plataforma = 'youtubeMusic'`).Scan(&conf)
	if conf != "manual" {
		t.Errorf("confianza = %q", conf)
	}
}
