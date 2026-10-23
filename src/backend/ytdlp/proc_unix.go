//go:build !windows

package ytdlp

import (
	"os/exec"
	"syscall"
	"time"
)

// configure hace que yt-dlp corra en su propio grupo de procesos y que cancelar
// el contexto le envíe SIGTERM a todo el grupo (el binario empaquetado lanza un hijo).
func configure(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	}
	cmd.WaitDelay = 5 * time.Second
}
