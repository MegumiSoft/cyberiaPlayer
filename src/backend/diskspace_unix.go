//go:build !windows

package backend

import "syscall"

// diskSpace devuelve el tamaño total y el espacio libre (en bytes) de la unidad que contiene path.
func diskSpace(path string) (total, free uint64) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0
	}
	bs := uint64(st.Bsize)
	return uint64(st.Blocks) * bs, uint64(st.Bavail) * bs
}
