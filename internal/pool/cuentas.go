package pool

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// ErrCuentaEnUso: esa cuenta de la plataforma ya está conectada por otra persona.
var ErrCuentaEnUso = errors.New("la cuenta ya está conectada por otro usuario")

// Cuenta es una cuenta de plataforma conectada por OAuth. Los tokens van
// cifrados; este paquete no los descifra.
type Cuenta struct {
	UsuarioID      int64
	Plataforma     string
	IDExterno      string
	NombreExterno  string
	AccessCifrado  []byte
	RefreshCifrado []byte
	Expira         time.Time
	Scopes         string
}

// GuardarCuenta crea o actualiza la cuenta del usuario en esa plataforma. Si
// refresh viene vacío, se conserva el anterior (Google no siempre lo reenvía).
func (s *Store) GuardarCuenta(ctx context.Context, c Cuenta) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO cuentas (usuario_id, plataforma, id_externo, nombre_externo,
		                     access_token_cifrado, refresh_token_cifrado, expira_en, scopes)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (usuario_id, plataforma) DO UPDATE SET
			id_externo = excluded.id_externo,
			nombre_externo = excluded.nombre_externo,
			access_token_cifrado = excluded.access_token_cifrado,
			refresh_token_cifrado = COALESCE(excluded.refresh_token_cifrado, cuentas.refresh_token_cifrado),
			expira_en = excluded.expira_en,
			scopes = excluded.scopes`,
		c.UsuarioID, c.Plataforma, c.IDExterno, nulo(c.NombreExterno),
		c.AccessCifrado, nuloBytes(c.RefreshCifrado), fecha(c.Expira), nulo(c.Scopes))
	if err != nil && strings.Contains(err.Error(), "cuentas.plataforma, cuentas.id_externo") {
		return ErrCuentaEnUso
	}
	return err
}

// ActualizarTokens guarda un access token renovado. Si refresh no viene
// vacío, también reemplaza el refresh token (Spotify puede rotarlo).
func (s *Store) ActualizarTokens(ctx context.Context, usuarioID int64, plataforma string, access, refresh []byte, expira time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE cuentas SET access_token_cifrado = ?, expira_en = ?,
		       refresh_token_cifrado = COALESCE(?, refresh_token_cifrado)
		WHERE usuario_id = ? AND plataforma = ?`, access, fecha(expira), nuloBytes(refresh), usuarioID, plataforma)
	return err
}

// CuentaDe devuelve la cuenta del usuario en la plataforma o ErrNoEncontrado.
func (s *Store) CuentaDe(ctx context.Context, usuarioID int64, plataforma string) (Cuenta, error) {
	c := Cuenta{UsuarioID: usuarioID, Plataforma: plataforma}
	var expira sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT id_externo, COALESCE(nombre_externo, ''), access_token_cifrado,
		       refresh_token_cifrado, expira_en, COALESCE(scopes, '')
		FROM cuentas WHERE usuario_id = ? AND plataforma = ?`, usuarioID, plataforma).
		Scan(&c.IDExterno, &c.NombreExterno, &c.AccessCifrado, &c.RefreshCifrado, &expira, &c.Scopes)
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrNoEncontrado
	}
	if err != nil {
		return c, err
	}
	if expira.Valid {
		c.Expira, _ = time.Parse(time.RFC3339, expira.String)
	}
	return c, nil
}

// InvalidarCuenta borra los tokens de una cuenta revocada o caducada, pero
// conserva su playlist espejo: al volver a conectar, se sigue usando.
func (s *Store) InvalidarCuenta(ctx context.Context, usuarioID int64, plataforma string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE cuentas SET access_token_cifrado = NULL, refresh_token_cifrado = NULL
		WHERE usuario_id = ? AND plataforma = ?`, usuarioID, plataforma)
	return err
}

// BorrarCuenta desconecta la plataforma: borra los tokens y deja de
// sincronizar sus playlists espejo (las playlists siguen en la plataforma).
func (s *Store) BorrarCuenta(ctx context.Context, usuarioID int64, plataforma string) error {
	return s.enTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			UPDATE ventana_items SET item_id_plataforma = NULL
			WHERE miembro_id IN (
				SELECT e.miembro_id FROM espejos e JOIN miembros m ON m.id = e.miembro_id
				WHERE m.usuario_id = ? AND e.plataforma = ?)`, usuarioID, plataforma); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE miembros SET plataforma_escucha = 'links'
			WHERE usuario_id = ? AND plataforma_escucha = ?`, usuarioID, plataforma); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM espejos WHERE plataforma = ?
			AND miembro_id IN (SELECT id FROM miembros WHERE usuario_id = ?)`, plataforma, usuarioID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx,
			`DELETE FROM cuentas WHERE usuario_id = ? AND plataforma = ?`, usuarioID, plataforma)
		return err
	})
}

func fecha(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC().Format(time.RFC3339)
}

func nuloBytes(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}
