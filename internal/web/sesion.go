package web

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"
)

const cookieSesion = "mp_sesion"

// sesiones firma con HMAC el id del usuario en una cookie. En F1 no hay
// login: una persona es "quien tiene esta cookie en este navegador".
type sesiones struct{ clave []byte }

func (s sesiones) firma(valor string) string {
	mac := hmac.New(sha256.New, s.clave)
	mac.Write([]byte(valor))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s sesiones) leer(r *http.Request) (int64, bool) {
	c, err := r.Cookie(cookieSesion)
	if err != nil {
		return 0, false
	}
	valor, firma, ok := strings.Cut(c.Value, ".")
	if !ok || !hmac.Equal([]byte(firma), []byte(s.firma(valor))) {
		return 0, false
	}
	id, err := strconv.ParseInt(valor, 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

func (s sesiones) establecer(w http.ResponseWriter, r *http.Request, usuarioID int64) {
	valor := strconv.FormatInt(usuarioID, 10)
	http.SetCookie(w, &http.Cookie{
		Name:     cookieSesion,
		Value:    valor + "." + s.firma(valor),
		Path:     "/",
		MaxAge:   400 * 24 * 60 * 60, // máximo que aceptan los navegadores
		HttpOnly: true,
		Secure:   esHTTPS(r),
		SameSite: http.SameSiteLaxMode,
	})
}

// esHTTPS considera el proxy (Caddy) que termina TLS delante del servidor.
func esHTTPS(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}
