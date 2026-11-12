package pool

import (
	"context"
	"database/sql"
	"errors"
	"sort"
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
			p.Espejo.Plataforma, plataformaMusica(p.Espejo.Plataforma), p.Espejo.Plataforma, grupoID, pos).
			Scan(&c.CancionID, &c.Titulo, &c.Artista, &c.ISRC, &c.DuracionS, &trackID, &c.SinCoincidencia)
		if err != nil {
			return p, err
		}
		c.TrackID = trackID.String
		if c.TrackID != "" {
			c.SinCoincidencia = false
		}
		p.Ventana = append(p.Ventana, c)
		if !enPlaylist[pos] {
			p.Agregar = append(p.Agregar, c)
		}
	}
	return p, nil
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
