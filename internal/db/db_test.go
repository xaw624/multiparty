package db

import (
	"context"
	"path/filepath"
	"testing"
)

func TestMigrateEsIdempotente(t *testing.T) {
	ctx := context.Background()
	database, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	for i := 0; i < 2; i++ {
		if err := Migrate(ctx, database); err != nil {
			t.Fatalf("pasada %d: %v", i+1, err)
		}
	}

	tablas := []string{"grupos", "usuarios", "miembros", "cuentas", "canciones",
		"mapeos", "aportes", "orden", "espejos", "ventana_items", "revision"}
	for _, tabla := range tablas {
		var n int
		err := database.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, tabla).Scan(&n)
		if err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Errorf("falta la tabla %q", tabla)
		}
	}

	var fk int
	if err := database.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&fk); err != nil {
		t.Fatal(err)
	}
	if fk != 1 {
		t.Error("foreign_keys no está activado")
	}
}
