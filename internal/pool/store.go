package pool

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
)

// Grupo es un conjunto de amigos con un pool común.
type Grupo struct {
	ID     int64
	Nombre string
	Codigo string
}

// Store guarda y consulta el estado del pool en SQLite.
//
// La base usa una sola conexión: dentro de una transacción todas las
// consultas deben ir por tx, nunca por s.db, o se bloquearía.
type Store struct{ db *sql.DB }

// NewStore crea un Store sobre una base ya migrada.
func NewStore(db *sql.DB) *Store { return &Store{db: db} }

func (s *Store) enTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// CrearUsuario registra a una persona con su nombre visible.
func (s *Store) CrearUsuario(ctx context.Context, nombre string) (int64, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO usuarios (nombre) VALUES (?)`, nombre)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// NombreUsuario devuelve el nombre de un usuario o ErrNoEncontrado.
func (s *Store) NombreUsuario(ctx context.Context, id int64) (string, error) {
	var nombre string
	err := s.db.QueryRowContext(ctx, `SELECT nombre FROM usuarios WHERE id = ?`, id).Scan(&nombre)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNoEncontrado
	}
	return nombre, err
}

const alfabetoCodigo = "abcdefghjkmnpqrstuvwxyz23456789"

func nuevoCodigo() string {
	b := make([]byte, 10)
	rand.Read(b) // nunca falla desde Go 1.24
	for i := range b {
		b[i] = alfabetoCodigo[int(b[i])%len(alfabetoCodigo)]
	}
	return string(b)
}

// CrearGrupo crea un grupo con un código de invitación y agrega al creador.
func (s *Store) CrearGrupo(ctx context.Context, nombre string, usuarioID int64) (Grupo, error) {
	g := Grupo{Nombre: nombre, Codigo: nuevoCodigo()}
	err := s.enTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`INSERT INTO grupos (nombre, codigo_invitacion) VALUES (?, ?)`, g.Nombre, g.Codigo)
		if err != nil {
			return err
		}
		if g.ID, err = res.LastInsertId(); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx,
			`INSERT INTO miembros (grupo_id, usuario_id, orden_turno) VALUES (?, ?, 0)`, g.ID, usuarioID)
		return err
	})
	return g, err
}

// GrupoPorCodigo busca un grupo por su código de invitación.
func (s *Store) GrupoPorCodigo(ctx context.Context, codigo string) (Grupo, error) {
	var g Grupo
	err := s.db.QueryRowContext(ctx,
		`SELECT id, nombre, codigo_invitacion FROM grupos WHERE codigo_invitacion = ?`, codigo).
		Scan(&g.ID, &g.Nombre, &g.Codigo)
	if errors.Is(err, sql.ErrNoRows) {
		return g, ErrNoEncontrado
	}
	return g, err
}

// GruposDeUsuario lista los grupos donde participa un usuario, el más nuevo primero.
func (s *Store) GruposDeUsuario(ctx context.Context, usuarioID int64) ([]Grupo, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT g.id, g.nombre, g.codigo_invitacion
		FROM grupos g JOIN miembros m ON m.grupo_id = g.id
		WHERE m.usuario_id = ?
		ORDER BY g.id DESC`, usuarioID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Grupo
	for rows.Next() {
		var g Grupo
		if err := rows.Scan(&g.ID, &g.Nombre, &g.Codigo); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// EsMiembro dice si el usuario pertenece al grupo.
func (s *Store) EsMiembro(ctx context.Context, grupoID, usuarioID int64) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM miembros WHERE grupo_id = ? AND usuario_id = ?`, grupoID, usuarioID).Scan(&n)
	return n > 0, err
}

// Unirse agrega al usuario al final de la rotación. Si ya es miembro, no hace nada.
func (s *Store) Unirse(ctx context.Context, grupoID, usuarioID int64) error {
	return s.enTx(ctx, func(tx *sql.Tx) error {
		if _, _, err := miembroTx(ctx, tx, grupoID, usuarioID); err == nil {
			return nil
		} else if !errors.Is(err, ErrNoEncontrado) {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO miembros (grupo_id, usuario_id, orden_turno)
			SELECT ?, ?, COALESCE(MAX(orden_turno), -1) + 1 FROM miembros WHERE grupo_id = ?`,
			grupoID, usuarioID, grupoID); err != nil {
			return err
		}
		return reordenar(ctx, tx, grupoID)
	})
}

// AgregarCancion aporta una canción al pool y recalcula el orden.
func (s *Store) AgregarCancion(ctx context.Context, grupoID, usuarioID int64, c CancionResuelta, origen Origen) error {
	if origen != Suelta && origen != Importada {
		return fmt.Errorf("origen desconocido %q", origen)
	}
	return s.enTx(ctx, func(tx *sql.Tx) error {
		if _, _, err := miembroTx(ctx, tx, grupoID, usuarioID); err != nil {
			if errors.Is(err, ErrNoEncontrado) {
				return ErrNoPermitido
			}
			return err
		}

		cancionID, err := buscarOCrearCancion(ctx, tx, c)
		if err != nil {
			return err
		}

		var por string
		err = tx.QueryRowContext(ctx, `
			SELECT u.nombre FROM aportes a JOIN usuarios u ON u.id = a.usuario_id
			WHERE a.grupo_id = ? AND a.cancion_id = ? AND a.estado != 'eliminado'
			LIMIT 1`, grupoID, cancionID).Scan(&por)
		if err == nil {
			return ErrDuplicada{Por: por}
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}

		consultaClave := `SELECT COALESCE(MIN(clave_fila), 0) - 1`
		if origen == Importada {
			consultaClave = `SELECT COALESCE(MAX(clave_fila), 0) + 1`
		}
		var clave int64
		if err := tx.QueryRowContext(ctx, consultaClave+`
			FROM aportes WHERE grupo_id = ? AND usuario_id = ? AND estado = 'pendiente'`,
			grupoID, usuarioID).Scan(&clave); err != nil {
			return err
		}

		if _, err := tx.ExecContext(ctx, `
			INSERT INTO aportes (grupo_id, cancion_id, usuario_id, origen, clave_fila)
			VALUES (?, ?, ?, ?, ?)`, grupoID, cancionID, usuarioID, string(origen), clave); err != nil {
			return err
		}
		return reordenar(ctx, tx, grupoID)
	})
}

// EliminarAporte quita un aporte propio que todavía no entró a ninguna ventana.
func (s *Store) EliminarAporte(ctx context.Context, grupoID, usuarioID, aporteID int64) error {
	return s.enTx(ctx, func(tx *sql.Tx) error {
		var dueno int64
		var estado string
		err := tx.QueryRowContext(ctx,
			`SELECT usuario_id, estado FROM aportes WHERE id = ? AND grupo_id = ?`, aporteID, grupoID).
			Scan(&dueno, &estado)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNoEncontrado
		}
		if err != nil {
			return err
		}
		if dueno != usuarioID || estado != "pendiente" {
			return ErrNoPermitido
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE aportes SET estado = 'eliminado' WHERE id = ?`, aporteID); err != nil {
			return err
		}
		return reordenar(ctx, tx, grupoID)
	})
}

// MarcarEscuchada libera una canción de la ventana del usuario: sale ella y
// entra la siguiente del orden común.
func (s *Store) MarcarEscuchada(ctx context.Context, grupoID, usuarioID int64, posicion int) error {
	return s.enTx(ctx, func(tx *sql.Tx) error {
		miembroID, cursor, err := miembroTx(ctx, tx, grupoID, usuarioID)
		if err != nil {
			if errors.Is(err, ErrNoEncontrado) {
				return ErrNoPermitido
			}
			return err
		}
		var total int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM orden WHERE grupo_id = ?`, grupoID).Scan(&total); err != nil {
			return err
		}
		esc, err := escuchadasDe(ctx, tx, miembroID, cursor)
		if err != nil {
			return err
		}
		if !contiene(Ventana(cursor, total, esc, TamVentana), posicion) {
			return ErrNoPermitido
		}

		if _, err := tx.ExecContext(ctx, `
			INSERT INTO ventana_items (miembro_id, posicion, estado, liberada_en)
			VALUES (?, ?, 'escuchada', datetime('now'))
			ON CONFLICT (miembro_id, posicion)
			DO UPDATE SET estado = 'escuchada', liberada_en = excluded.liberada_en`,
			miembroID, posicion); err != nil {
			return err
		}

		esc[posicion] = true
		for esc[cursor] {
			cursor++
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE miembros SET cursor = ? WHERE id = ?`, cursor, miembroID); err != nil {
			return err
		}
		return reordenar(ctx, tx, grupoID)
	})
}

// CambiarPlataformaLinks guarda en qué plataforma quiere abrir los links el usuario.
func (s *Store) CambiarPlataformaLinks(ctx context.Context, grupoID, usuarioID int64, plataforma string) error {
	if !PlataformaValida(plataforma) {
		return ErrNoPermitido
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE miembros SET plataforma_links = ? WHERE grupo_id = ? AND usuario_id = ?`,
		nulo(plataforma), grupoID, usuarioID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNoPermitido
	}
	return nil
}

func miembroTx(ctx context.Context, tx *sql.Tx, grupoID, usuarioID int64) (id int64, cursor int, err error) {
	err = tx.QueryRowContext(ctx,
		`SELECT id, cursor FROM miembros WHERE grupo_id = ? AND usuario_id = ?`, grupoID, usuarioID).
		Scan(&id, &cursor)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNoEncontrado
	}
	return id, cursor, err
}

func escuchadasDe(ctx context.Context, tx *sql.Tx, miembroID int64, desde int) (map[int]bool, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT posicion FROM ventana_items
		WHERE miembro_id = ? AND estado = 'escuchada' AND posicion >= ?`, miembroID, desde)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	esc := map[int]bool{}
	for rows.Next() {
		var p int
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		esc[p] = true
	}
	return esc, rows.Err()
}

// buscarOCrearCancion reutiliza la canción si ya existe por ISRC o por el id
// en alguna plataforma, y guarda todos sus enlaces.
func buscarOCrearCancion(ctx context.Context, tx *sql.Tx, c CancionResuelta) (int64, error) {
	var id int64
	if c.ISRC != "" {
		err := tx.QueryRowContext(ctx, `SELECT id FROM canciones WHERE isrc = ?`, c.ISRC).Scan(&id)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return 0, err
		}
	}
	for _, e := range c.Enlaces {
		if id != 0 {
			break
		}
		if e.TrackID == "" {
			continue
		}
		err := tx.QueryRowContext(ctx,
			`SELECT cancion_id FROM mapeos WHERE plataforma = ? AND track_id = ? LIMIT 1`,
			e.Plataforma, e.TrackID).Scan(&id)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return 0, err
		}
	}

	if id == 0 {
		titulo := c.Titulo
		if titulo == "" {
			titulo = "(sin título)"
		}
		res, err := tx.ExecContext(ctx,
			`INSERT INTO canciones (isrc, titulo, artista, duracion_ms, portada_url) VALUES (?, ?, ?, ?, ?)`,
			nulo(c.ISRC), titulo, c.Artista, nuloInt(c.DuracionMS), nulo(c.PortadaURL))
		if err != nil {
			return 0, err
		}
		if id, err = res.LastInsertId(); err != nil {
			return 0, err
		}
	}

	if err := guardarEnlaces(ctx, tx, id, c.Enlaces); err != nil {
		return 0, err
	}
	return id, nil
}

// guardarEnlaces inserta o actualiza los mapeos de una canción.
func guardarEnlaces(ctx context.Context, tx *sql.Tx, cancionID int64, enlaces []Enlace) error {
	for _, e := range enlaces {
		if e.URL == "" {
			continue
		}
		confianza := e.Confianza
		if confianza == "" {
			confianza = Busqueda
		}
		// Un enlace directo reemplaza a uno encontrado por búsqueda, no al revés.
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO mapeos (cancion_id, plataforma, track_id, url, confianza)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT (cancion_id, plataforma) DO UPDATE SET
				track_id = excluded.track_id,
				url = excluded.url,
				confianza = excluded.confianza,
				verificado_en = datetime('now')
			WHERE mapeos.confianza NOT IN ('directo', 'manual') OR excluded.confianza IN ('directo', 'manual')`,
			cancionID, e.Plataforma, nulo(e.TrackID), e.URL, string(confianza)); err != nil {
			return err
		}
	}
	return nil
}

func nulo(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nuloInt(n int) any {
	if n == 0 {
		return nil
	}
	return n
}

func contiene(xs []int, x int) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
