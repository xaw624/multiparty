package pool_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/xaw624/multiparty/internal/db"
	"github.com/xaw624/multiparty/internal/pool"
)

func nuevoStore(t *testing.T) (*pool.Store, func(grupoID int64) []string) {
	t.Helper()
	ctx := context.Background()
	database, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if err := db.Migrate(ctx, database); err != nil {
		t.Fatal(err)
	}
	orden := func(grupoID int64) []string {
		rows, err := database.Query(`
			SELECT c.titulo FROM orden o
			JOIN aportes a ON a.id = o.aporte_id JOIN canciones c ON c.id = a.cancion_id
			WHERE o.grupo_id = ? ORDER BY o.posicion`, grupoID)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var s string
			rows.Scan(&s)
			out = append(out, s)
		}
		return out
	}
	return pool.NewStore(database), orden
}

func cancion(titulo string) pool.CancionResuelta {
	return pool.CancionResuelta{
		Titulo:  titulo,
		Artista: "Artista",
		Enlaces: []pool.Enlace{
			{Plataforma: "spotify", TrackID: "sp-" + titulo, URL: "https://open.spotify.com/track/" + titulo},
			{Plataforma: "songlink", URL: "https://song.link/s/" + titulo},
		},
	}
}

func debe(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestOrdenVentanasYPosicionesFijas(t *testing.T) {
	ctx := context.Background()
	s, orden := nuevoStore(t)

	ana, err := s.CrearUsuario(ctx, "Ana")
	debe(t, err)
	beto, err := s.CrearUsuario(ctx, "Beto")
	debe(t, err)
	g, err := s.CrearGrupo(ctx, "Amigos", ana)
	debe(t, err)
	debe(t, s.Unirse(ctx, g.ID, beto))

	// Ana llena las ventanas (10) y deja 2 en la cola.
	for i := 1; i <= 12; i++ {
		debe(t, s.AgregarCancion(ctx, g.ID, ana, cancion(fmt.Sprintf("A%02d", i)), pool.Suelta))
	}
	got := orden(g.ID)
	// Las dos que no caben en la ventana quedan en cola; la suelta más nueva va primero.
	if got[10] != "A12" || got[11] != "A11" {
		t.Fatalf("cola de Ana = %v", got[10:])
	}

	// Beto aporta: entra en el siguiente turno, intercalado con la cola de Ana.
	debe(t, s.AgregarCancion(ctx, g.ID, beto, cancion("B1"), pool.Suelta))
	debe(t, s.AgregarCancion(ctx, g.ID, beto, cancion("B2"), pool.Suelta))
	if want := []string{"B2", "A12", "B1", "A11"}; !reflect.DeepEqual(orden(g.ID)[10:], want) {
		t.Fatalf("cola = %v, quiere %v", orden(g.ID)[10:], want)
	}

	// Ana escucha la primera: su ventana avanza y B2 (posición 10) queda fija.
	debe(t, s.MarcarEscuchada(ctx, g.ID, ana, 0))
	v, err := s.Vista(ctx, g.ID, ana)
	debe(t, err)
	if len(v.Ventana) != 10 || v.Ventana[9].Titulo != "B2" {
		t.Fatalf("ventana de Ana termina en %q", v.Ventana[len(v.Ventana)-1].Titulo)
	}

	// B2 ya está en una ventana: no se puede quitar. B1 sigue en cola: sí.
	var aporteB1, aporteB2 int64
	vb, err := s.Vista(ctx, g.ID, beto)
	debe(t, err)
	for _, it := range vb.MisPendientes {
		if it.Titulo == "B1" {
			aporteB1 = it.AporteID
		}
		if it.Titulo == "B2" {
			aporteB2 = it.AporteID
		}
	}
	if aporteB2 != 0 {
		t.Error("B2 no debería figurar como pendiente")
	}
	if aporteB1 == 0 {
		t.Fatal("B1 debería figurar como pendiente")
	}
	debe(t, s.EliminarAporte(ctx, g.ID, beto, aporteB1))
	if want := []string{"B2", "A12", "A11"}; !reflect.DeepEqual(orden(g.ID)[10:], want) {
		t.Fatalf("tras eliminar, cola = %v", orden(g.ID)[10:])
	}

	// Ana no puede quitar aportes ajenos.
	if err := s.EliminarAporte(ctx, g.ID, ana, aporteB1); !errors.Is(err, pool.ErrNoPermitido) {
		t.Errorf("eliminar ajeno: err = %v", err)
	}

	// No se puede agregar dos veces la misma canción.
	var dup pool.ErrDuplicada
	if err := s.AgregarCancion(ctx, g.ID, beto, cancion("A01"), pool.Suelta); !errors.As(err, &dup) || dup.Por != "Ana" {
		t.Errorf("duplicada: err = %v", err)
	}

	// Solo se puede marcar lo que está en la propia ventana.
	if err := s.MarcarEscuchada(ctx, g.ID, beto, 11); !errors.Is(err, pool.ErrNoPermitido) {
		t.Errorf("marcar fuera de la ventana: err = %v", err)
	}

	// Escuchar fuera de orden: el cursor salta solo cuando no quedan huecos.
	debe(t, s.MarcarEscuchada(ctx, g.ID, beto, 2))
	vb, _ = s.Vista(ctx, g.ID, beto)
	if vb.Yo.Cursor != 0 || vb.Ventana[0].Titulo != "A01" || vb.Ventana[2].Titulo != "A04" {
		t.Fatalf("cursor = %d, ventana empieza %q, %q", vb.Yo.Cursor, vb.Ventana[0].Titulo, vb.Ventana[2].Titulo)
	}
	debe(t, s.MarcarEscuchada(ctx, g.ID, beto, 0))
	debe(t, s.MarcarEscuchada(ctx, g.ID, beto, 1))
	vb, _ = s.Vista(ctx, g.ID, beto)
	if vb.Yo.Cursor != 3 {
		t.Errorf("cursor = %d, quiere 3", vb.Yo.Cursor)
	}
}

func TestPoolChicoSeAgregaAlFinal(t *testing.T) {
	ctx := context.Background()
	s, orden := nuevoStore(t)
	ana, _ := s.CrearUsuario(ctx, "Ana")
	beto, _ := s.CrearUsuario(ctx, "Beto")
	g, err := s.CrearGrupo(ctx, "Amigos", ana)
	debe(t, err)
	debe(t, s.Unirse(ctx, g.ID, beto))

	// Con ventanas incompletas cada canción entra de inmediato y queda fija.
	for _, c := range []struct {
		u int64
		t string
	}{{ana, "A1"}, {ana, "A2"}, {beto, "B1"}} {
		debe(t, s.AgregarCancion(ctx, g.ID, c.u, cancion(c.t), pool.Suelta))
	}
	if want := []string{"A1", "A2", "B1"}; !reflect.DeepEqual(orden(g.ID), want) {
		t.Errorf("orden = %v, quiere %v", orden(g.ID), want)
	}
}

func TestLinksSegunPlataforma(t *testing.T) {
	ctx := context.Background()
	s, _ := nuevoStore(t)
	ana, _ := s.CrearUsuario(ctx, "Ana")
	g, _ := s.CrearGrupo(ctx, "Amigos", ana)
	debe(t, s.AgregarCancion(ctx, g.ID, ana, cancion("X"), pool.Suelta))

	v, err := s.Vista(ctx, g.ID, ana)
	debe(t, err)
	if v.Ventana[0].URL != "https://song.link/s/X" {
		t.Errorf("sin preferencia: URL = %q", v.Ventana[0].URL)
	}

	debe(t, s.CambiarPlataformaLinks(ctx, g.ID, ana, "spotify"))
	v, _ = s.Vista(ctx, g.ID, ana)
	if v.Ventana[0].URL != "https://open.spotify.com/track/X" {
		t.Errorf("con Spotify: URL = %q", v.Ventana[0].URL)
	}

	// Sin enlace en la plataforma elegida, cae a song.link.
	debe(t, s.CambiarPlataformaLinks(ctx, g.ID, ana, "deezer"))
	v, _ = s.Vista(ctx, g.ID, ana)
	if v.Ventana[0].URL != "https://song.link/s/X" {
		t.Errorf("con Deezer: URL = %q", v.Ventana[0].URL)
	}

	if err := s.CambiarPlataformaLinks(ctx, g.ID, ana, "napster"); !errors.Is(err, pool.ErrNoPermitido) {
		t.Errorf("plataforma inválida: err = %v", err)
	}
}
