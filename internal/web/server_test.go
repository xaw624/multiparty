package web

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xaw624/multiparty/internal/db"
	"github.com/xaw624/multiparty/internal/pool"
)

// resolvedorFalso responde con canciones fijas según el link.
type resolvedorFalso map[string]pool.CancionResuelta

func (f resolvedorFalso) Resolver(_ context.Context, enlace string) (pool.CancionResuelta, error) {
	if c, ok := f[enlace]; ok {
		return c, nil
	}
	return pool.CancionResuelta{}, pool.ErrNoEncontrada
}

func cancionFalsa(titulo string) pool.CancionResuelta {
	return pool.CancionResuelta{
		Titulo:  titulo,
		Artista: "Artista",
		Enlaces: []pool.Enlace{
			{Plataforma: "spotify", TrackID: "sp-" + titulo, URL: "https://open.spotify.com/track/" + titulo},
			{Plataforma: "songlink", URL: "https://song.link/s/" + titulo},
		},
	}
}

func servidorPrueba(t *testing.T) *httptest.Server {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "web.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if err := db.Migrate(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	res := resolvedorFalso{
		"https://open.spotify.com/track/1": cancionFalsa("Canción Uno"),
		"https://open.spotify.com/track/2": cancionFalsa("Canción Dos"),
	}
	srv, err := New(database, slog.New(slog.NewTextHandler(io.Discard, nil)), Opciones{Resolver: res, Secreto: []byte("clave-de-prueba")})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)
	return ts
}

func cliente(t *testing.T) *http.Client {
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{Jar: jar}
}

func post(t *testing.T, c *http.Client, u string, form url.Values, htmx bool) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, u, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func debeContener(t *testing.T, cuerpo string, textos ...string) {
	t.Helper()
	for _, s := range textos {
		if !strings.Contains(cuerpo, s) {
			t.Errorf("la respuesta no contiene %q", s)
		}
	}
}

func TestFlujoCompleto(t *testing.T) {
	ts := servidorPrueba(t)
	ana, beto := cliente(t), cliente(t)

	// Ana crea el grupo y termina en su página.
	resp, cuerpo := post(t, ana, ts.URL+"/grupos",
		url.Values{"nombre_grupo": {"Amigos"}, "tu_nombre": {"Ana"}}, false)
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Request.URL.Path, "/g/") {
		t.Fatalf("crear grupo: %d %s", resp.StatusCode, resp.Request.URL)
	}
	debeContener(t, cuerpo, "Amigos", "Hola, Ana")
	urlGrupo := ts.URL + resp.Request.URL.Path

	// Ana agrega una canción con HTMX: recibe solo el fragmento.
	_, cuerpo = post(t, ana, urlGrupo+"/aportes", url.Values{"enlace": {"https://open.spotify.com/track/1"}}, true)
	debeContener(t, cuerpo, `id="grupo"`, "Canción Uno", "Agregaste")
	if strings.Contains(cuerpo, "<html") {
		t.Error("la respuesta HTMX no debe incluir el layout")
	}

	// Link desconocido: mensaje de error y el link se conserva en el formulario.
	_, cuerpo = post(t, ana, urlGrupo+"/aportes", url.Values{"enlace": {"https://x.test/nada"}}, true)
	debeContener(t, cuerpo, "No encontré esa canción", `value="https://x.test/nada"`)

	// Sin sesión no se puede aportar.
	resp, _ = post(t, beto, urlGrupo+"/aportes", url.Values{"enlace": {"https://open.spotify.com/track/2"}}, true)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("aportar sin unirse: %d", resp.StatusCode)
	}

	// Beto ve la invitación, se une y ve la canción de Ana en su ventana.
	resp, err := beto.Get(urlGrupo)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	debeContener(t, string(b), "Te invitaron")

	_, cuerpo = post(t, beto, urlGrupo+"/unirse", url.Values{"tu_nombre": {"Beto"}}, false)
	debeContener(t, cuerpo, "Hola, Beto", "Canción Uno", "de Ana")

	// Duplicada.
	_, cuerpo = post(t, beto, urlGrupo+"/aportes", url.Values{"enlace": {"https://open.spotify.com/track/1"}}, true)
	debeContener(t, cuerpo, "ya está en el pool (la agregó Ana)")

	// Beto la marca como escuchada y su ventana queda vacía.
	_, cuerpo = post(t, beto, urlGrupo+"/escuchada/0", nil, true)
	debeContener(t, cuerpo, "No tienes canciones por escuchar")

	// Grupo inexistente.
	resp, _ = beto.Get(ts.URL + "/g/noexiste")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("grupo inexistente: %d", resp.StatusCode)
	}
}

func TestRechazaPostDeOtroSitio(t *testing.T) {
	ts := servidorPrueba(t)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/grupos",
		strings.NewReader(url.Values{"nombre_grupo": {"X"}, "tu_nombre": {"Y"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("POST de otro sitio: %d, quiere 403", resp.StatusCode)
	}
}
