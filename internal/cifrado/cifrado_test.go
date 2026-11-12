package cifrado

import (
	"bytes"
	"errors"
	"testing"
)

func TestSellarYAbrir(t *testing.T) {
	c, err := New(bytes.Repeat([]byte{7}, TamClave))
	if err != nil {
		t.Fatal(err)
	}
	a, b := c.Sellar([]byte("token")), c.Sellar([]byte("token"))
	if bytes.Equal(a, b) {
		t.Error("dos cifrados del mismo texto no deben ser iguales")
	}
	got, err := c.Abrir(a)
	if err != nil || string(got) != "token" {
		t.Fatalf("Abrir = %q, %v", got, err)
	}

	a[len(a)-1] ^= 1
	if _, err := c.Abrir(a); !errors.Is(err, ErrDatos) {
		t.Errorf("datos alterados: err = %v", err)
	}

	otra, _ := New(bytes.Repeat([]byte{8}, TamClave))
	if _, err := otra.Abrir(b); !errors.Is(err, ErrDatos) {
		t.Errorf("otra clave: err = %v", err)
	}
	if _, err := New([]byte("corta")); err == nil {
		t.Error("una clave corta debe fallar")
	}
}
