package pool

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// Revision es una canción que no se pudo emparejar sola en una plataforma.
type Revision struct {
	ID         int64
	CancionID  int64
	Titulo     string
	Artista    string
	Plataforma string
	Candidatos []Candidato
}

// RevisionesDeGrupo lista las revisiones abiertas de canciones del grupo.
func (s *Store) RevisionesDeGrupo(ctx context.Context, grupoID int64) ([]Revision, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT r.id, c.id, c.titulo, c.artista, r.plataforma, r.candidatos_json
		FROM revision r JOIN canciones c ON c.id = r.cancion_id
		WHERE r.estado = 'abierta'
		  AND r.cancion_id IN (SELECT cancion_id FROM aportes WHERE grupo_id = ? AND estado != 'eliminado')
		ORDER BY r.id`, grupoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Revision
	for rows.Next() {
		var r Revision
		var j string
		if err := rows.Scan(&r.ID, &r.CancionID, &r.Titulo, &r.Artista, &r.Plataforma, &j); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(j), &r.Candidatos) // datos propios; si fallan, sin candidatos
		out = append(out, r)
	}
	return out, rows.Err()
}

// RevisionDeGrupo devuelve una revisión abierta si su canción está en el grupo.
func (s *Store) RevisionDeGrupo(ctx context.Context, grupoID, revisionID int64) (Revision, error) {
	revs, err := s.RevisionesDeGrupo(ctx, grupoID)
	if err != nil {
		return Revision{}, err
	}
	for _, r := range revs {
		if r.ID == revisionID {
			return r, nil
		}
	}
	return Revision{}, ErrNoEncontrado
}

// ResolverRevision guarda los enlaces elegidos a mano y cierra la revisión.
func (s *Store) ResolverRevision(ctx context.Context, revisionID, usuarioID int64, enlaces []Enlace) error {
	return s.enTx(ctx, func(tx *sql.Tx) error {
		var cancionID int64
		err := tx.QueryRowContext(ctx,
			`SELECT cancion_id FROM revision WHERE id = ? AND estado = 'abierta'`, revisionID).Scan(&cancionID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNoEncontrado
		}
		if err != nil {
			return err
		}
		for i := range enlaces {
			enlaces[i].Confianza = Manual
		}
		if err := guardarEnlaces(ctx, tx, cancionID, enlaces); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx,
			`UPDATE revision SET estado = 'resuelta', resuelto_por = ? WHERE id = ?`, usuarioID, revisionID)
		return err
	})
}

// ReintentarRevision borra la revisión para que la próxima sincronización
// vuelva a buscar la canción (por ejemplo, si salió después en la plataforma).
func (s *Store) ReintentarRevision(ctx context.Context, revisionID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM revision WHERE id = ? AND estado = 'abierta'`, revisionID)
	return err
}
