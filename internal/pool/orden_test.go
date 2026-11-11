package pool

import (
	"reflect"
	"testing"
)

func TestIntercalar(t *testing.T) {
	casos := []struct {
		nombre string
		turnos []int64
		ultimo int64
		filas  map[int64][]int64
		quiere []int64
	}{
		{
			nombre: "turnos parejos",
			turnos: []int64{1, 2},
			filas:  map[int64][]int64{1: {10, 11}, 2: {20, 21}},
			quiere: []int64{10, 20, 11, 21},
		},
		{
			nombre: "empieza después del último fijo",
			turnos: []int64{1, 2, 3},
			ultimo: 2,
			filas:  map[int64][]int64{1: {10}, 2: {20}, 3: {30}},
			quiere: []int64{30, 10, 20},
		},
		{
			nombre: "quien no tiene pendientes pierde el turno",
			turnos: []int64{1, 2, 3},
			filas:  map[int64][]int64{1: {10, 11, 12}, 3: {30}},
			quiere: []int64{10, 30, 11, 12},
		},
		{
			nombre: "ignora filas de no miembros",
			turnos: []int64{1},
			filas:  map[int64][]int64{1: {10}, 9: {90}},
			quiere: []int64{10},
		},
		{
			nombre: "sin pendientes",
			turnos: []int64{1, 2},
			filas:  map[int64][]int64{},
			quiere: []int64{},
		},
	}
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			got := Intercalar(c.turnos, c.ultimo, c.filas)
			if !reflect.DeepEqual(got, c.quiere) {
				t.Errorf("Intercalar = %v, quiere %v", got, c.quiere)
			}
		})
	}
}

func TestVentanaYFrontera(t *testing.T) {
	esc := map[int]bool{3: true, 4: true}
	pos := Ventana(2, 20, esc, 5)
	if want := []int{2, 5, 6, 7, 8}; !reflect.DeepEqual(pos, want) {
		t.Fatalf("Ventana = %v, quiere %v", pos, want)
	}
	if f, ok := Frontera(pos, 5); !ok || f != 9 {
		t.Errorf("Frontera = %d, %v; quiere 9, true", f, ok)
	}

	corta := Ventana(0, 3, nil, 5)
	if len(corta) != 3 {
		t.Fatalf("Ventana corta = %v", corta)
	}
	if _, ok := Frontera(corta, 5); ok {
		t.Error("una ventana incompleta no debe tener frontera")
	}
}
