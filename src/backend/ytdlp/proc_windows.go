//go:build windows

package ytdlp

import (
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

const createNoWindow = 0x08000000

// configure oculta la ventana de consola y, al cancelar, mata todo el árbol de procesos:
// el yt-dlp.exe empaquetado lanza un proceso hijo que sobreviviría a un Kill normal.
func configure(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		kill := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid))
		kill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
		return kill.Run()
	}
	cmd.WaitDelay = 5 * time.Second
}
