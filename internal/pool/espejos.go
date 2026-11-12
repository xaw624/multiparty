package pool

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"time"
)

// Espejo es la playlist de un miembro en su plataforma de escucha.
type Espejo struct {
	MiembroID  int64
	Plataforma string
	PlaylistID string
}

// URL abre la playlist en la app de la plataforma.
func (e Espejo) URL() string {
	switch e.Plataforma {
	case "youtube":
		return "https://music.youtube.com/playlist?list=" + e.PlaylistID
	case "spotify":
		return "https://open.spotify.com/playlist/" + e.PlaylistID
	}
	return ""
}

// ItemPlaylist es una canción que hoy está en la playlist espejo.
type ItemPlaylist struct {
	Posicion int
	ItemID   string
}

// CancionEspejo es una canción de la ventana que falta en la playlist.
type CancionEspejo struct {
	Posicion        int
	CancionID       int64
	Titulo          string
	Artista         string
	ISRC            string
	DuracionS       int
	TrackID         string // id en la plataforma; vacío si aún no se emparejó
	SinCoincidencia bool   // ya se buscó y no se encontró
}

// PlanEspejo dice qué cambiar en la playlist para que coincida con la ventana.
type PlanEspejo struct {
	Espejo  Espejo
	Ventana []CancionEspejo // toda la ventana, en orden
	Quitar  []ItemPlaylist  // está en la playlist y ya no debería
	Agregar []CancionEspejo // falta en la playlist (subconjunto de Ventana)
}

// Candidato es una posible coincidencia guardada para la revisión manual.
type Candidato struct {
	ID      string
	Titulo  string
	Detalle string // artista o canal
	URL     string
}

// Pendientes cuenta los cambios que una sincronización haría.
func (p PlanEspejo) Pendientes() int {
	n := len(p.Quitar)
	for _, a := range p.Agregar {
		if !a.SinCoincidencia {
			n++
		}
	}
	return n
}

// CrearEspejo registra la playlist del miembro y la vuelve su plataforma de escucha.
func (s *Store) CrearEspejo(ctx context.Context, grupoID, usuarioID int64, plataforma, playlistID string) error {
	return s.enTx(ctx, func(tx *sql.Tx) error {
		miembroID, _, err := miembroTx(ctx, tx, grupoID, usuarioID)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO espejos (miembro_id, plataforma, playlist_id) VALUES (?, ?, ?)
			ON CONFLICT (miembro_id) DO UPDATE SET
				plataforma = excluded.plataforma, playlist_id = excluded.playlist_id`,
			miembroID, plataforma, playlistID); err != nil {
			return err
		}
		// Una playlist nueva empieza vacía.
		if _, err := tx.ExecContext(ctx,
			`UPDATE ventana_items SET item_id_plataforma = NULL WHERE miembro_id = ?`, miembroID); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx,
			`UPDATE miembros SET plataforma_escucha = ? WHERE id = ?`, plataforma, miembroID)
		return err
	})
}

// EspejoDe devuelve la playlist espejo del usuario en el grupo o ErrNoEncontrado.
func (s *Store) EspejoDe(ctx context.Context, grupoID, usuarioID int64) (Espejo, error) {
	var e Espejo
	err := s.db.QueryRowContext(ctx, `
		SELECT e.miembro_id, e.plataforma, e.playlist_id
		FROM espejos e JOIN miembros m ON m.id = e.miembro_id
		WHERE m.grupo_id = ? AND m.usuario_id = ?`, grupoID, usuarioID).
		Scan(&e.MiembroID, &e.Plataforma, &e.PlaylistID)
	if errors.Is(err, sql.ErrNoRows) {
		return e, ErrNoEncontrado
	}
	return e, err
}

// PlanDeEspejo calcula qué agregar y quitar de la playlist del usuario.
func (s *Store) PlanDeEspejo(ctx context.Context, grupoID, usuarioID int64) (PlanEspejo, error) {
	var p PlanEspejo
	err := s.enTx(ctx, func(tx *sql.Tx) error {
		var err error
		p, err = planEspejoTx(ctx, tx, grupoID, usuarioID)
		return err
	})
	return p, err
}

func planEspejoTx(ctx context.Context, tx *sql.Tx, grupoID, usuarioID int64) (PlanEspejo, error) {
	var p PlanEspejo
	var cursor int
	err := tx.QueryRowContext(ctx, `
		SELECT e.miembro_id, e.plataforma, e.playlist_id, m.cursor
		FROM espejos e JOIN miembros m ON m.id = e.miembro_id
		WHERE m.grupo_id = ? AND m.usuario_id = ?`, grupoID, usuarioID).
		Scan(&p.Espejo.MiembroID, &p.Espejo.Plataforma, &p.Espejo.PlaylistID, &cursor)
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNoEncontrado
	}
	if err != nil {
		return p, err
	}

	var total int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM orden WHERE grupo_id = ?`, grupoID).Scan(&total); err != nil {
		return p, err
	}
	esc, err := escuchadasDe(ctx, tx, p.Espejo.MiembroID, cursor)
	if err != nil {
		return p, err
	}
	ventana := map[int]bool{}
	for _, pos := range Ventana(cursor, total, esc, TamVentana) {
		ventana[pos] = true
	}

	// Lo que hoy está en la playlist.
	rows, err := tx.QueryContext(ctx, `
		SELECT posicion, item_id_plataforma, estado FROM ventana_items
		WHERE miembro_id = ? AND item_id_plataforma IS NOT NULL`, p.Espejo.MiembroID)
	if err != nil {
		return p, err
	}
	enPlaylist := map[int]bool{}
	for rows.Next() {
		var it ItemPlaylist
		var estado string
		if err := rows.Scan(&it.Posicion, &it.ItemID, &estado); err != nil {
			rows.Close()
			return p, err
		}
		enPlaylist[it.Posicion] = true
		if estado == "escuchada" || !ventana[it.Posicion] {
			p.Quitar = append(p.Quitar, it)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return p, err
	}

	// Toda la ventana en orden; lo que falta en la playlist va a Agregar.
	var posiciones []int
	for pos := range ventana {
		posiciones = append(posiciones, pos)
	}
	sort.Ints(posiciones)
	for _, pos := range posiciones {
		c, err := cancionEspejoTx(ctx, tx, grupoID, pos, p.Espejo.Plataforma)
		if err != nil {
			return p, err
		}
		p.Ventana = append(p.Ventana, c)
		if !enPlaylist[pos] {
			p.Agregar = append(p.Agregar, c)
		}
	}
	return p, nil
}

// cancionEspejoTx devuelve la canción de una posición con su id en la
// plataforma (si ya se emparejó) y si quedó sin coincidencia.
func cancionEspejoTx(ctx context.Context, tx *sql.Tx, grupoID int64, pos int, plataforma string) (CancionEspejo, error) {
	c := CancionEspejo{Posicion: pos}
	var trackID sql.NullString
	err := tx.QueryRowContext(ctx, `
		SELECT c.id, c.titulo, c.artista, COALESCE(c.isrc, ''), COALESCE(c.duracion_ms, 0) / 1000,
		       (SELECT track_id FROM mapeos
		         WHERE cancion_id = c.id AND plataforma IN (?, ?) AND track_id IS NOT NULL
		         ORDER BY plataforma DESC LIMIT 1),
		       EXISTS (SELECT 1 FROM revision
		         WHERE cancion_id = c.id AND plataforma = ? AND estado = 'abierta')
		FROM orden o
		JOIN aportes a ON a.id = o.aporte_id
		JOIN canciones c ON c.id = a.cancion_id
		WHERE o.grupo_id = ? AND o.posicion = ?`,
		plataforma, plataformaMusica(plataforma), plataforma, grupoID, pos).
		Scan(&c.CancionID, &c.Titulo, &c.Artista, &c.ISRC, &c.DuracionS, &trackID, &c.SinCoincidencia)
	if err != nil {
		return c, err
	}
	c.TrackID = trackID.String
	if c.TrackID != "" {
		c.SinCoincidencia = false
	}
	return c, nil
}

// plataformaMusica es la variante "de música" de una plataforma: el mismo id
// de video sirve en YouTube y en YouTube Music.
func plataformaMusica(p string) string {
	if p == "youtube" {
		return "youtubeMusic"
	}
	return p
}

// ItemAgregado registra que la canción de esa posición ya está en la playlist.
func (s *Store) ItemAgregado(ctx context.Context, miembroID int64, posicion int, itemID string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO ventana_items (miembro_id, posicion, item_id_plataforma, estado)
		VALUES (?, ?, ?, 'activa')
		ON CONFLICT (miembro_id, posicion) DO UPDATE SET item_id_plataforma = excluded.item_id_plataforma`,
		miembroID, posicion, itemID)
	return err
}

// EspejoReemplazado registra que la playlist quedó con exactamente esos
// elementos (posición → id del elemento), como tras un reemplazo en Spotify.
func (s *Store) EspejoReemplazado(ctx context.Context, miembroID int64, items map[int]string) error {
	return s.enTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`UPDATE ventana_items SET item_id_plataforma = NULL WHERE miembro_id = ?`, miembroID); err != nil {
			return err
		}
		for pos, id := range items {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO ventana_items (miembro_id, posicion, item_id_plataforma, estado)
				VALUES (?, ?, ?, 'activa')
				ON CONFLICT (miembro_id, posicion) DO UPDATE SET item_id_plataforma = excluded.item_id_plataforma`,
				miembroID, pos, id); err != nil {
				return err
			}
		}
		return nil
	})
}

// ItemQuitado registra que la canción de esa posición ya no está en la playlist.
func (s *Store) ItemQuitado(ctx context.Context, miembroID int64, posicion int) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE ventana_items SET item_id_plataforma = NULL WHERE miembro_id = ? AND posicion = ?`,
		miembroID, posicion)
	return err
}

// GuardarEnlaces agrega o actualiza enlaces de una canción existente.
func (s *Store) GuardarEnlaces(ctx context.Context, cancionID int64, enlaces []Enlace) error {
	return s.enTx(ctx, func(tx *sql.Tx) error {
		return guardarEnlaces(ctx, tx, cancionID, enlaces)
	})
}

// SinCoincidencia deja la canción en revisión para esa plataforma, para no
// volver a gastar una búsqueda en ella.
func (s *Store) SinCoincidencia(ctx context.Context, cancionID int64, plataforma, candidatosJSON string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO revision (cancion_id, plataforma, candidatos_json)
		SELECT ?, ?, ? WHERE NOT EXISTS (
			SELECT 1 FROM revision WHERE cancion_id = ? AND plataforma = ? AND estado = 'abierta')`,
		cancionID, plataforma, candidatosJSON, cancionID, plataforma)
	return err
}

// EspejoMiembro identifica la playlist espejo de una persona en un grupo.
type EspejoMiembro struct {
	GrupoID   int64
	UsuarioID int64
}

// EspejosDe lista todas las playlists espejo de una plataforma.
func (s *Store) EspejosDe(ctx context.Context, plataforma string) ([]EspejoMiembro, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT m.grupo_id, m.usuario_id FROM espejos e JOIN miembros m ON m.id = e.miembro_id
		WHERE e.plataforma = ? ORDER BY m.usuario_id, m.grupo_id`, plataforma)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EspejoMiembro
	for rows.Next() {
		var e EspejoMiembro
		if err := rows.Scan(&e.GrupoID, &e.UsuarioID); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// MarcarEscuchadaPorTrack marca como escuchada la canción de la ventana del
// usuario que tenga ese id en la plataforma. Dice si encontró alguna.
func (s *Store) MarcarEscuchadaPorTrack(ctx context.Context, grupoID, usuarioID int64, trackID string) (bool, error) {
	plan, err := s.PlanDeEspejo(ctx, grupoID, usuarioID)
	if err != nil {
		return false, err
	}
	for _, c := range plan.Ventana {
		if c.TrackID == trackID {
			return true, s.MarcarEscuchada(ctx, grupoID, usuarioID, c.Posicion)
		}
	}
	return false, nil
}

// PorEmparejar es una canción de una ventana (o a punto de entrar) que aún no
// tiene id en la plataforma.
type PorEmparejar struct {
	UsuarioID int64
	Cancion   CancionEspejo
}

// ProximasSinEmparejar lista, en todos los espejos de la plataforma, las
// canciones de la ventana y de las siguientes adelanto posiciones que aún no
// se emparejaron ni quedaron en revisión. Cada canción aparece una vez.
func (s *Store) ProximasSinEmparejar(ctx context.Context, plataforma string, adelanto int) ([]PorEmparejar, error) {
	espejos, err := s.EspejosDe(ctx, plataforma)
	if err != nil {
		return nil, err
	}
	var out []PorEmparejar
	vistas := map[int64]bool{}
	err = s.enTx(ctx, func(tx *sql.Tx) error {
		for _, e := range espejos {
			miembroID, cursor, err := miembroTx(ctx, tx, e.GrupoID, e.UsuarioID)
			if err != nil {
				return err
			}
			var total int
			if err := tx.QueryRowContext(ctx,
				`SELECT COUNT(*) FROM orden WHERE grupo_id = ?`, e.GrupoID).Scan(&total); err != nil {
				return err
			}
			esc, err := escuchadasDe(ctx, tx, miembroID, cursor)
			if err != nil {
				return err
			}
			for _, pos := range Ventana(cursor, total, esc, TamVentana+adelanto) {
				c, err := cancionEspejoTx(ctx, tx, e.GrupoID, pos, plataforma)
				if err != nil {
					return err
				}
				if c.TrackID != "" || c.SinCoincidencia || vistas[c.CancionID] {
					continue
				}
				vistas[c.CancionID] = true
				out = append(out, PorEmparejar{UsuarioID: e.UsuarioID, Cancion: c})
			}
		}
		return nil
	})
	return out, err
}

// MapeoViejo es un id de YouTube que hay que volver a verificar.
type MapeoViejo struct {
	CancionID int64
	TrackID   string
}

// MapeosPorVerificar lista hasta n ids de video verificados antes de antes.
func (s *Store) MapeosPorVerificar(ctx context.Context, antes time.Time, n int) ([]MapeoViejo, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT cancion_id, track_id FROM mapeos
		WHERE plataforma IN ('youtube', 'youtubeMusic') AND track_id IS NOT NULL
		  AND verificado_en < ?
		ORDER BY verificado_en LIMIT ?`, antes.UTC().Format("2006-01-02 15:04:05"), n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MapeoViejo
	for rows.Next() {
		var m MapeoViejo
		if err := rows.Scan(&m.CancionID, &m.TrackID); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// VideoVerificado renueva la fecha de verificación de un video que sigue
// existiendo, o borra sus mapeos si ya no existe.
func (s *Store) VideoVerificado(ctx context.Context, videoID string, existe bool) error {
	consulta := `UPDATE mapeos SET verificado_en = datetime('now')
		WHERE plataforma IN ('youtube', 'youtubeMusic') AND track_id = ?`
	if !existe {
		consulta = `DELETE FROM mapeos WHERE plataforma IN ('youtube', 'youtubeMusic') AND track_id = ?`
	}
	_, err := s.db.ExecContext(ctx, consulta, videoID)
	return err
}
