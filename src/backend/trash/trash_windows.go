//go:build windows

package trash

import (
	"errors"
	"syscall"
	"unsafe"
)

const (
	foDelete          = 0x0003
	fofSilent         = 0x0004
	fofNoConfirmation = 0x0010
	fofAllowUndo      = 0x0040
	fofNoErrorUI      = 0x0400
)

// SHFILEOPSTRUCTW (alineación de Windows 64 bits y 32 bits resuelta por el compilador).
type shFileOp struct {
	Hwnd                 uintptr
	Func                 uint32
	From                 *uint16
	To                   *uint16
	Flags                uint16
	AnyOperationsAborted int32
	NameMappings         uintptr
	ProgressTitle        *uint16
}

func moveToTrash(abs string) error {
	from, err := syscall.UTF16FromString(abs)
	if err != nil {
		return err
	}
	from = append(from, 0) // la lista termina con doble NUL
	op := shFileOp{Func: foDelete, From: &from[0], Flags: fofAllowUndo | fofNoConfirmation | fofSilent | fofNoErrorUI}
	proc := syscall.NewLazyDLL("shell32.dll").NewProc("SHFileOperationW")
	r, _, _ := proc.Call(uintptr(unsafe.Pointer(&op)))
	if r != 0 {
		return errors.New("Windows no pudo enviar el archivo a la papelera")
	}
	if op.AnyOperationsAborted != 0 {
		return errors.New("operación cancelada")
	}
	return nil
}
