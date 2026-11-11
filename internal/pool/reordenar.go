package pool

import (
	"context"
	"database/sql"
)

type miembroOrden struct {
	id      int64
	usuario int64
	cursor  int
}

type posOrden struct {
	aporte  int64
	usuario int64
	estado  string
}

// reordenar recalcula el orden común del grupo.
//
// Las posiciones que ya están dentro de la ventana de alguien son fijas: no se
// mueven y su aporte pasa a 'ordenado'. Lo que queda después de la ventana más
// avanzada (la cola) se vuelve a armar por turnos con todos los aportes
// pendientes. Ver docs/PLAN.md, sección 3.
func reordenar(ctx context.Context, tx *sql.Tx, grupoID int64) error {
	miembros, err := cargarMiembros(ctx, tx, grupoID)
	if err != nil {
		return err
	}
	orden, err := cargarOrden(ctx, tx, grupoID)
	if err != nil {
		return err
	}
	esc, err := cargarEscuchadas(ctx, tx, grupoID)
	if err != nil {
		return err
	}

	// 1. Fijar lo que ya cae dentro de alguna ventana.
	fijas := frontera(miembros, esc, len(orden))
	for fijas < len(orden) && orden[fijas].estado == "ordenado" {
		fijas++ // lo ya fijado nunca vuelve a la cola
	}
	if err := congelar(ctx, tx, aportesDe(orden[:fijas])); err != nil {
		return err
	}

	// 2. Rearmar la cola por turnos, siguiendo al último usuario fijo.
	var ultimo int64
	if fijas > 0 {
		ultimo = orden[fijas-1].usuario
	}
	filas, err := cargarPendientes(ctx, tx, grupoID)
	if err != nil {
		return err
	}
	turnos := make([]int64, len(miembros))
	for i, m := range miembros {
		turnos[i] = m.usuario
	}
	cola := Intercalar(turnos, ultimo, filas)

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM orden WHERE grupo_id = ? AND posicion >= ?`, grupoID, fijas); err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO orden (grupo_id, posicion, aporte_id) VALUES (?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for i, aporte := range cola {
		if _, err := stmt.ExecContext(ctx, grupoID, fijas+i, aporte); err != nil {
			return err
		}
	}

	// 3. Las canciones nuevas pueden haber entrado a una ventana incompleta.
	total := fijas + len(cola)
	if nuevasFijas := frontera(miembros, esc, total); nuevasFijas > fijas {
		return congelar(ctx, tx, cola[:nuevasFijas-fijas])
	}
	return nil
}

// frontera es la primera posición que no está en la ventana de nadie.
func frontera(miembros []miembroOrden, esc map[int64]map[int]bool, total int) int {
	f := 0
	for _, m := range miembros {
		lim, llena := Frontera(Ventana(m.cursor, total, esc[m.id], TamVentana), TamVentana)
		if !llena {
			return total
		}
		f = max(f, lim)
	}
	return min(f, total)
}

func congelar(ctx context.Context, tx *sql.Tx, aportes []int64) error {
	for _, id := range aportes {
		if _, err := tx.ExecContext(ctx,
			`UPDATE aportes SET estado = 'ordenado' WHERE id = ? AND estado = 'pendiente'`, id); err != nil {
			return err
		}
	}
	return nil
}

func aportesDe(ps []posOrden) []int64 {
	out := make([]int64, len(ps))
	for i, p := range ps {
		out[i] = p.aporte
	}
	return out
}

func cargarMiembros(ctx context.Context, tx *sql.Tx, grupoID int64) ([]miembroOrden, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT id, usuario_id, cursor FROM miembros WHERE grupo_id = ? ORDER BY orden_turno`, grupoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []miembroOrden
	for rows.Next() {
		var m miembroOrden
		if err := rows.Scan(&m.id, &m.usuario, &m.cursor); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func cargarOrden(ctx context.Context, tx *sql.Tx, grupoID int64) ([]posOrden, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT o.aporte_id, a.usuario_id, a.estado
		FROM orden o JOIN aportes a ON a.id = o.aporte_id
		WHERE o.grupo_id = ? ORDER BY o.posicion`, grupoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []posOrden
	for rows.Next() {
		var p posOrden
		if err := rows.Scan(&p.aporte, &p.usuario, &p.estado); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func cargarEscuchadas(ctx context.Context, tx *sql.Tx, grupoID int64) (map[int64]map[int]bool, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT vi.miembro_id, vi.posicion
		FROM ventana_items vi JOIN miembros m ON m.id = vi.miembro_id
		WHERE m.grupo_id = ? AND vi.estado = 'escuchada' AND vi.posicion >= m.cursor`, grupoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]map[int]bool{}
	for rows.Next() {
		var m int64
		var p int
		if err := rows.Scan(&m, &p); err != nil {
			return nil, err
		}
		if out[m] == nil {
			out[m] = map[int]bool{}
		}
		out[m][p] = true
	}
	return out, rows.Err()
}

// cargarPendientes devuelve, por usuario, sus aportes pendientes en el orden
// de su fila personal.
func cargarPendientes(ctx context.Context, tx *sql.Tx, grupoID int64) (map[int64][]int64, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT id, usuario_id FROM aportes
		WHERE grupo_id = ? AND estado = 'pendiente'
		ORDER BY clave_fila, id`, grupoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][]int64{}
	for rows.Next() {
		var id, u int64
		if err := rows.Scan(&id, &u); err != nil {
			return nil, err
		}
		out[u] = append(out[u], id)
	}
	return out, rows.Err()
}
