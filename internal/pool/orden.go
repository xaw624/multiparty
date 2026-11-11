package pool

// Intercalar arma el orden por turnos.
//
// turnos son los usuarios en su orden de rotación. ultimo es el usuario de la
// última posición ya fija (0 si no hay ninguna): el siguiente turno es el de
// quien le sigue. filas tiene, por usuario, sus aportes pendientes ya
// ordenados. Quien no tiene pendientes pierde su turno. Se ignoran las filas
// de usuarios que no están en turnos.
func Intercalar(turnos []int64, ultimo int64, filas map[int64][]int64) []int64 {
	n := len(turnos)
	if n == 0 {
		return nil
	}

	inicio := 0
	for i, u := range turnos {
		if u == ultimo {
			inicio = i + 1
			break
		}
	}

	cola := make(map[int64][]int64, n)
	restantes := 0
	for _, u := range turnos {
		cola[u] = filas[u]
		restantes += len(filas[u])
	}

	out := make([]int64, 0, restantes)
	for i := inicio; restantes > 0; i++ {
		u := turnos[i%n]
		if f := cola[u]; len(f) > 0 {
			out = append(out, f[0])
			cola[u] = f[1:]
			restantes--
		}
	}
	return out
}

// Ventana devuelve las posiciones que ve una persona: las primeras n
// posiciones desde cursor que no ha escuchado, sin pasar de total.
func Ventana(cursor, total int, escuchadas map[int]bool, n int) []int {
	pos := make([]int, 0, n)
	for p := cursor; p < total && len(pos) < n; p++ {
		if !escuchadas[p] {
			pos = append(pos, p)
		}
	}
	return pos
}

// Frontera devuelve la primera posición después de una ventana llena. Si la
// ventana no está llena devuelve false: cualquier canción nueva entraría en
// ella, así que no hay límite.
func Frontera(pos []int, n int) (int, bool) {
	if n > 0 && len(pos) == n {
		return pos[n-1] + 1, true
	}
	return 0, false
}
