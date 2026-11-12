// Paquete cifrado guarda secretos (tokens OAuth) cifrados con AES-256-GCM.
package cifrado

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
)

// TamClave es el tamaño de la clave en bytes (AES-256).
const TamClave = 32

// ErrDatos indica que el texto cifrado está dañado o se cifró con otra clave.
var ErrDatos = errors.New("datos cifrados inválidos")

// Caja cifra y descifra con una clave fija.
type Caja struct{ aead cipher.AEAD }

// New crea una Caja con una clave de 32 bytes.
func New(clave []byte) (*Caja, error) {
	if len(clave) != TamClave {
		return nil, fmt.Errorf("la clave debe tener %d bytes, tiene %d", TamClave, len(clave))
	}
	bloque, err := aes.NewCipher(clave)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(bloque)
	if err != nil {
		return nil, err
	}
	return &Caja{aead: aead}, nil
}

// DesdeBase64 crea una Caja con una clave en base64 (por ejemplo, la salida
// de `openssl rand -base64 32`).
func DesdeBase64(s string) (*Caja, error) {
	clave, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("la clave no es base64 válido: %w", err)
	}
	return New(clave)
}

// Sellar cifra datos. El resultado lleva el nonce al principio.
func (c *Caja) Sellar(datos []byte) []byte {
	nonce := make([]byte, c.aead.NonceSize())
	rand.Read(nonce) // nunca falla desde Go 1.24
	return c.aead.Seal(nonce, nonce, datos, nil)
}

// Abrir descifra lo que produjo Sellar.
func (c *Caja) Abrir(sellado []byte) ([]byte, error) {
	n := c.aead.NonceSize()
	if len(sellado) < n {
		return nil, ErrDatos
	}
	datos, err := c.aead.Open(nil, sellado[:n], sellado[n:], nil)
	if err != nil {
		return nil, ErrDatos
	}
	return datos, nil
}
