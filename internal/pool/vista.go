package pool

import (
	"context"
	"database/sql"
	"errors"
)

// CuantasDespues es cuántas canciones se muestran después de la ventana.
const CuantasDespues = 5

// Item es una canción del orden común, lista para mostrarse.
type Item struct {
	Posicion int
	AporteID int64
	Titulo   string
	Artista  string
	Portada  string
	URL      string // link en la plataforma preferida o página de song.link
	Por      string // nombre de quien la aportó
	EsMio    bool
	// Solo con playlist espejo:
	EnPlaylist      bool // ya está en la playlist
	SinCoincidencia bool // no se encontró en la plataforma del espejo
	usuarioID       int64
	cancionID       int64
	estado          string
}

// Yo es el miembro que está viendo la página.
type Yo struct {
	MiembroID       int64
	Nombre          string
	Cursor          int
	PlataformaLinks string
}

// ResumenMiembro es una fila de la lista de miembros.
type ResumenMiembro struct {
	Nombre     string
	Pendientes int
	Escuchadas int
	EsYo       bool
}

// Vista es todo lo que muestra la página de un grupo para un miembro.
type Vista struct {
	Grupo         Grupo
	Yo            Yo
	Ventana       []Item
	Despues       []Item
	MisPendientes []Item
	Miembros      []ResumenMiembro
	TotalPool     int
	Plataformas   []Plataforma
	// CuentaYouTube es el nombre del canal conectado; vacío si no hay.
	CuentaYouTube string
	// Espejo es la playlist del miembro; nil si escucha con links.
	Espejo *Espejo
	// Pendientes son los cambios que haría "Sincronizar".
	Pendientes int
}

// Vista arma la página del grupo para el usuario. Devuelve ErrNoPermitido si
// no es miembro.
func (s *Store) Vista(ctx context.Context, grupoID, usuarioID int64) (Vista, error) {
	v := Vista{Plataformas: PlataformasLinks}
	err := s.enTx(ctx, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx,
			`SELECT id, nombre, codigo_invitacion FROM grupos WHERE id = ?`, grupoID).
			Scan(&v.Grupo.ID, &v.Grupo.Nombre, &v.Grupo.Codigo)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNoEncontrado
		}
		if err != nil {
			return err
		}

		err = tx.QueryRowContext(ctx, `
			SELECT m.id, m.cursor, COALESCE(m.plataforma_links, ''), u.nombre
			FROM miembros m JOIN usuarios u ON u.id = m.usuario_id
			WHERE m.grupo_id = ? AND m.usuario_id = ?`, grupoID, usuarioID).
			Scan(&v.Yo.MiembroID, &v.Yo.Cursor, &v.Yo.PlataformaLinks, &v.Yo.Nombre)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNoPermitido
		}
		if err != nil {
			return err
		}

		items, err := cargarItems(ctx, tx, grupoID, v.Yo.PlataformaLinks)
		if err != nil {
			return err
		}
		v.TotalPool = len(items)
		for i := range items {
			items[i].EsMio = items[i].usuarioID == usuarioID
		}

		esc, err := escuchadasDe(ctx, tx, v.Yo.MiembroID, v.Yo.Cursor)
		if err != nil {
			return err
		}
		ventana := Ventana(v.Yo.Cursor, len(items), esc, TamVentana)
		for _, p := range ventana {
			v.Ventana = append(v.Ventana, items[p])
		}
		if lim, llena := Frontera(ventana, TamVentana); llena {
			for _, p := range Ventana(lim, len(items), esc, CuantasDespues) {
				v.Despues = append(v.Despues, items[p])
			}
		}
		for _, it := range items {
			if it.EsMio && it.estado == "pendiente" {
				v.MisPendientes = append(v.MisPendientes, it)
			}
		}

		v.Miembros, err = cargarResumen(ctx, tx, grupoID, usuarioID)
		if err != nil {
			return err
		}

		err = tx.QueryRowContext(ctx, `
			SELECT COALESCE(nombre_externo, id_externo) FROM cuentas
			WHERE usuario_id = ? AND plataforma = 'youtube' AND refresh_token_cifrado IS NOT NULL`,
			usuarioID).Scan(&v.CuentaYouTube)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}

		plan, err := planEspejoTx(ctx, tx, grupoID, usuarioID)
		if errors.Is(err, ErrNoEncontrado) {
			return nil
		}
		if err != nil {
			return err
		}
		v.Espejo = &plan.Espejo
		v.Pendientes = plan.Pendientes()
		falta := map[int]CancionEspejo{}
		for _, a := range plan.Agregar {
			falta[a.Posicion] = a
		}
		for i := range v.Ventana {
			a, pendiente := falta[v.Ventana[i].Posicion]
			v.Ventana[i].EnPlaylist = !pendiente
			v.Ventana[i].SinCoincidencia = pendiente && a.SinCoincidencia
		}
		return nil
	})
	return v, err
}

// cargarItems devuelve el orden común completo; el índice es la posición.
func cargarItems(ctx context.Context, tx *sql.Tx, grupoID int64, plataforma string) ([]Item, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT o.posicion, a.id, a.usuario_id, a.estado, u.nombre,
		       c.id, c.titulo, c.artista, COALESCE(c.portada_url, '')
		FROM orden o
		JOIN aportes a ON a.id = o.aporte_id
		JOIN canciones c ON c.id = a.cancion_id
		JOIN usuarios u ON u.id = a.usuario_id
		WHERE o.grupo_id = ?
		ORDER BY o.posicion`, grupoID)
	if err != nil {
		return nil, err
	}
	var items []Item
	for rows.Next() {
		var it Item
		if err := rows.Scan(&it.Posicion, &it.AporteID, &it.usuarioID, &it.estado, &it.Por,
			&it.cancionID, &it.Titulo, &it.Artista, &it.Portada); err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, it)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	urls, err := cargarURLs(ctx, tx, grupoID, plataforma)
	if err != nil {
		return nil, err
	}
	for i := range items {
		u := urls[items[i].cancionID]
		if items[i].URL = u[plataforma]; items[i].URL == "" {
			items[i].URL = u["songlink"]
		}
	}
	return items, nil
}

func cargarURLs(ctx context.Context, tx *sql.Tx, grupoID int64, plataforma string) (map[int64]map[string]string, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT m.cancion_id, m.plataforma, m.url FROM mapeos m
		WHERE m.cancion_id IN (SELECT cancion_id FROM aportes WHERE grupo_id = ?)
		  AND m.plataforma IN (?, 'songlink') AND m.url IS NOT NULL`, grupoID, plataforma)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]map[string]string{}
	for rows.Next() {
		var id int64
		var p, u string
		if err := rows.Scan(&id, &p, &u); err != nil {
			return nil, err
		}
		if out[id] == nil {
			out[id] = map[string]string{}
		}
		out[id][p] = u
	}
	return out, rows.Err()
}

func cargarResumen(ctx context.Context, tx *sql.Tx, grupoID, usuarioID int64) ([]ResumenMiembro, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT u.nombre, m.usuario_id,
		       (SELECT COUNT(*) FROM aportes a
		         WHERE a.grupo_id = m.grupo_id AND a.usuario_id = m.usuario_id AND a.estado = 'pendiente'),
		       (SELECT COUNT(*) FROM ventana_items vi
		         WHERE vi.miembro_id = m.id AND vi.estado = 'escuchada')
		FROM miembros m JOIN usuarios u ON u.id = m.usuario_id
		WHERE m.grupo_id = ?
		ORDER BY m.orden_turno`, grupoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ResumenMiembro
	for rows.Next() {
		var r ResumenMiembro
		var uid int64
		if err := rows.Scan(&r.Nombre, &uid, &r.Pendientes, &r.Escuchadas); err != nil {
			return nil, err
		}
		r.EsYo = uid == usuarioID
		out = append(out, r)
	}
	return out, rows.Err()
}
