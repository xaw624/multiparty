// Paquete tareas corre trabajos periódicos en segundo plano:
//
//   - Escuchas de Spotify: marca como escuchado lo que la persona oyó en
//     Spotify y actualiza su playlist (nivel 1: la ventana avanza sola).
//   - Pre-emparejar: busca por adelantado, en la plataforma de cada espejo,
//     las canciones que están por entrar a una ventana.
//   - Refrescar YouTube: vuelve a verificar cada 30 días los ids de video
//     guardados (política de YouTube III.E.4) y borra los que ya no existen.
//
// En YouTube las tareas solo leen o buscan; nunca modifican la cuenta del
// usuario (eso ocurre solo por acción suya).
package tareas

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"time"

	"github.com/xaw624/multiparty/internal/conexion"
	"github.com/xaw624/multiparty/internal/espejo"
	"github.com/xaw624/multiparty/internal/pool"
	"github.com/xaw624/multiparty/internal/spotify"
	"github.com/xaw624/multiparty/internal/youtube"
)

const (
	// adelanto es cuántas canciones después de la ventana se pre-emparejan.
	adelanto = 5
	// Cuántas búsquedas por corrida: en YouTube cada una cuesta 101 unidades.
	maxBusquedasYouTube = 3
	maxBusquedasSpotify = 10
	// vigenciaYouTube es cada cuánto se verifican los ids de video.
	vigenciaYouTube = 30 * 24 * time.Hour
)

// Tareas tiene lo necesario para los trabajos periódicos.
type Tareas struct {
	st    *pool.Store
	con   *conexion.Conexiones
	log   *slog.Logger
	ahora func() time.Time
}

// New crea las tareas.
func New(st *pool.Store, con *conexion.Conexiones, log *slog.Logger) *Tareas {
	return &Tareas{st: st, con: con, log: log, ahora: time.Now}
}

// Correr ejecuta las tareas periódicamente hasta que ctx termine.
func (t *Tareas) Correr(ctx context.Context) {
	if t.con.Habilitada(conexion.Spotify) {
		go t.cada(ctx, "escuchas de Spotify", 3*time.Minute, t.EscuchasSpotify)
	}
	if t.con.Alguna() {
		go t.cada(ctx, "pre-emparejar", 15*time.Minute, t.Preemparejar)
	}
	if t.con.Habilitada(conexion.YouTube) {
		go t.cada(ctx, "refrescar YouTube", 6*time.Hour, t.RefrescarYouTube)
	}
}

func (t *Tareas) cada(ctx context.Context, nombre string, periodo time.Duration, f func(context.Context) error) {
	espera := time.NewTimer(30 * time.Second) // deja arrancar el servidor
	defer espera.Stop()
	select {
	case <-ctx.Done():
		return
	case <-espera.C:
	}
	tick := time.NewTicker(periodo)
	defer tick.Stop()
	for {
		corrida, cancel := context.WithTimeout(ctx, periodo)
		if err := f(corrida); err != nil && ctx.Err() == nil {
			t.log.Warn("tarea fallida", "tarea", nombre, "err", err)
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// EscuchasSpotify revisa lo escuchado por cada persona con playlist en
// Spotify, marca lo que estaba en su ventana y actualiza su playlist.
func (t *Tareas) EscuchasSpotify(ctx context.Context) error {
	espejos, err := t.st.EspejosDe(ctx, conexion.Spotify)
	if err != nil {
		return err
	}
	porUsuario := map[int64][]int64{}
	for _, e := range espejos {
		porUsuario[e.UsuarioID] = append(porUsuario[e.UsuarioID], e.GrupoID)
	}
	for uid, grupos := range porUsuario {
		if err := t.escuchasDe(ctx, uid, grupos); err != nil {
			t.manejar(ctx, uid, conexion.Spotify, err)
		}
	}
	return nil
}

func (t *Tareas) escuchasDe(ctx context.Context, uid int64, grupos []int64) error {
	desde, ok, err := t.st.CursorEscuchas(ctx, uid, conexion.Spotify)
	if err != nil {
		return err
	}
	if !ok {
		// Primera vez: solo cuenta lo que se escuche de aquí en adelante.
		return t.st.GuardarCursorEscuchas(ctx, uid, conexion.Spotify, t.ahora())
	}
	sp, guardar, err := t.con.Spotify(ctx, uid)
	if err != nil {
		return err
	}
	defer guardar()
	escuchas, err := sp.Escuchadas(ctx, desde)
	if err != nil {
		return err
	}
	if len(escuchas) == 0 {
		return nil
	}
	sort.Slice(escuchas, func(i, j int) bool { return escuchas[i].Cuando.Before(escuchas[j].Cuando) })

	cambiados := map[int64]bool{}
	for _, e := range escuchas {
		for _, g := range grupos {
			marcada, err := t.st.MarcarEscuchadaPorTrack(ctx, g, uid, e.TrackID)
			if err != nil {
				return err
			}
			if marcada {
				cambiados[g] = true
			}
		}
	}
	for g := range cambiados {
		if _, err := espejo.SincronizarSpotify(ctx, t.st, sp, g, uid); err != nil {
			return err
		}
	}
	return t.st.GuardarCursorEscuchas(ctx, uid, conexion.Spotify, escuchas[len(escuchas)-1].Cuando)
}

// Preemparejar busca por adelantado las canciones que pronto entrarán a una
// ventana, para que «Sincronizar» sea rápido y no gaste búsquedas de golpe.
func (t *Tareas) Preemparejar(ctx context.Context) error {
	for _, p := range []string{conexion.Spotify, conexion.YouTube} {
		if !t.con.Habilitada(p) {
			continue
		}
		pendientes, err := t.st.ProximasSinEmparejar(ctx, p, adelanto)
		if err != nil {
			return err
		}
		limite := maxBusquedasSpotify
		if p == conexion.YouTube {
			limite = maxBusquedasYouTube
		}
		for i, pe := range pendientes {
			if i == limite {
				break
			}
			if err := t.emparejar(ctx, p, pe); err != nil {
				t.manejar(ctx, pe.UsuarioID, p, err)
				if errors.Is(err, youtube.ErrCuota) || errors.Is(err, spotify.ErrLimite) {
					break
				}
			}
		}
	}
	return nil
}

func (t *Tareas) emparejar(ctx context.Context, p string, pe pool.PorEmparejar) error {
	switch p {
	case conexion.YouTube:
		yt, guardar, err := t.con.YouTube(ctx, pe.UsuarioID)
		if err != nil {
			return err
		}
		defer guardar()
		_, err = espejo.EmparejarYouTube(ctx, t.st, yt, pe.Cancion)
		return err
	case conexion.Spotify:
		sp, guardar, err := t.con.Spotify(ctx, pe.UsuarioID)
		if err != nil {
			return err
		}
		defer guardar()
		_, err = espejo.EmparejarSpotify(ctx, t.st, sp, pe.Cancion)
		return err
	}
	return nil
}

// RefrescarYouTube verifica los ids de video guardados hace más de 30 días
// (50 por corrida, 1 unidad de cuota) y borra los que ya no existen.
func (t *Tareas) RefrescarYouTube(ctx context.Context) error {
	viejos, err := t.st.MapeosPorVerificar(ctx, t.ahora().Add(-vigenciaYouTube), 50)
	if err != nil || len(viejos) == 0 {
		return err
	}
	uid, ok, err := t.st.AlgunaCuenta(ctx, conexion.YouTube)
	if err != nil || !ok {
		return err
	}
	yt, guardar, err := t.con.YouTube(ctx, uid)
	if err != nil {
		return err
	}
	defer guardar()
	ids := make([]string, 0, len(viejos))
	vistos := map[string]bool{}
	for _, m := range viejos {
		if !vistos[m.TrackID] {
			vistos[m.TrackID] = true
			ids = append(ids, m.TrackID)
		}
	}
	existen, err := yt.Duraciones(ctx, ids)
	if err != nil {
		return err
	}
	for _, id := range ids {
		_, existe := existen[id]
		if err := t.st.VideoVerificado(ctx, id, existe); err != nil {
			return err
		}
	}
	return nil
}

// manejar registra el error y, si el token ya no sirve, marca la cuenta para
// que la persona vuelva a conectar.
func (t *Tareas) manejar(ctx context.Context, uid int64, p string, err error) {
	switch {
	case errors.Is(err, conexion.ErrNoConectado):
		return
	case errors.Is(err, spotify.ErrReconectar), errors.Is(err, youtube.ErrReconectar):
		if e := t.st.InvalidarCuenta(context.WithoutCancel(ctx), uid, p); e != nil {
			t.log.Warn("invalidar cuenta", "err", e)
		}
	}
	t.log.Info("tarea: no se pudo procesar", "plataforma", p, "usuario", uid, "err", err)
}
