// Paquete db abre la base SQLite y aplica las migraciones embebidas.
package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	_ "modernc.org/sqlite" // driver "sqlite", Go puro (sin CGO)
)

//go:embed migrations/*.sql
var migrations embed.FS

// Open abre (o crea) la base en path con los pragmas recomendados.
func Open(path string) (*sql.DB, error) {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, fmt.Errorf("crear directorio de la base: %w", err)
		}
	}
	dsn := "file:" + path +
		"?_pragma=foreign_keys(1)" +
		"&_pragma=journal_mode(WAL)" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=synchronous(NORMAL)"
	database, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("abrir sqlite: %w", err)
	}
	// SQLite admite un solo escritor; una conexión evita errores SQLITE_BUSY.
	database.SetMaxOpenConns(1)
	if err := database.Ping(); err != nil {
		database.Close()
		return nil, fmt.Errorf("conectar a sqlite: %w", err)
	}
	return database, nil
}

// Migrate aplica, en orden, los archivos migrations/NNNN_*.sql pendientes.
// Cada migración corre en su propia transacción.
func Migrate(ctx context.Context, database *sql.DB) error {
	if _, err := database.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version     TEXT PRIMARY KEY,
			aplicada_en TEXT NOT NULL DEFAULT (datetime('now'))
		)`); err != nil {
		return fmt.Errorf("crear schema_migrations: %w", err)
	}

	names, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)

	for _, name := range names {
		version := strings.TrimSuffix(filepath.Base(name), ".sql")

		var exists int
		err := database.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, version).Scan(&exists)
		if err != nil {
			return fmt.Errorf("consultar migración %s: %w", version, err)
		}
		if exists > 0 {
			continue
		}

		body, err := migrations.ReadFile(name)
		if err != nil {
			return err
		}
		if err := apply(ctx, database, version, string(body)); err != nil {
			return fmt.Errorf("migración %s: %w", version, err)
		}
	}
	return nil
}

func apply(ctx context.Context, database *sql.DB, version, body string) error {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, body); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations (version) VALUES (?)`, version); err != nil {
		return err
	}
	return tx.Commit()
}

// Respaldar copia la base completa y consistente a destino (que no debe
// existir), aunque el servidor esté funcionando. Usa VACUUM INTO.
func Respaldar(ctx context.Context, database *sql.DB, destino string) error {
	if _, err := os.Stat(destino); err == nil {
		return fmt.Errorf("el respaldo %s ya existe", destino)
	}
	if err := os.MkdirAll(filepath.Dir(destino), 0o750); err != nil {
		return err
	}
	_, err := database.ExecContext(ctx, `VACUUM INTO ?`, destino)
	return err
}
