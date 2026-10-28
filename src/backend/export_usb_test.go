package backend

import "testing"

func TestCamelotTables(t *testing.T) {
	// La menor = 8A, Do mayor = 8B, Re menor = 7A, Fa# menor = 11A, Mi mayor = 12B.
	if camelotMinor[9] != 8 || camelotMajor[0] != 8 || camelotMinor[2] != 7 || camelotMinor[6] != 11 || camelotMajor[4] != 12 {
		t.Fatal("tabla Camelot incorrecta")
	}
	// Cada rueda usa los números 1-12 una sola vez.
	for _, tb := range [][]int{camelotMinor, camelotMajor} {
		seen := map[int]bool{}
		for _, n := range tb {
			seen[n] = true
		}
		if len(seen) != 12 {
			t.Fatal("números repetidos")
		}
	}
	// Relativos: Do mayor (8B) y La menor (8A) comparten número.
	for pc := 0; pc < 12; pc++ {
		if camelotMajor[pc] != camelotMinor[(pc+9)%12] {
			t.Fatalf("relativo de pc %d", pc)
		}
	}
}
