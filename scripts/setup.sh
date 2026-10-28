#!/usr/bin/env bash
# Instala lo necesario para compilar y usar Cyberia desde el código.
# macOS: Terminal.   Windows: Git Bash (viene con Git for Windows).
# Uso:  ./scripts/setup.sh
set -u
cd "$(dirname "$0")/.."

case "$(uname -s)" in
  Darwin) OS=mac ;;
  MINGW* | MSYS* | CYGWIN*) OS=win ;;
  *) OS=linux ;;
esac

have() { command -v "$1" >/dev/null 2>&1; }
say() { printf '\n\033[1;36m==> %s\033[0m\n' "$*"; }
warn() { printf '\033[1;33m!! %s\033[0m\n' "$*"; }
NEED_RESTART=0

# ---------------------------------------------------------------- gestores de paquetes
install_pkg() { # install_pkg <comando> <brew> <winget-id>
  local cmd=$1 brew_pkg=$2 winget_id=$3
  have "$cmd" && return 0
  say "Instalando $cmd"
  if [ "$OS" = mac ]; then
    have brew || { warn "Falta Homebrew. Instálalo desde https://brew.sh y vuelve a ejecutar este script."; return 1; }
    brew install "$brew_pkg"
  elif [ "$OS" = win ]; then
    have winget || { warn "Falta winget (App Installer de Microsoft Store). Instala $cmd a mano y vuelve a ejecutar."; return 1; }
    winget install -e --id "$winget_id" --accept-package-agreements --accept-source-agreements
    NEED_RESTART=1
  else
    warn "Instala $cmd con el gestor de tu distribución."; return 1
  fi
}

# ---------------------------------------------------------------- Go, Node
install_pkg go go GoLang.Go
install_pkg node node OpenJS.NodeJS.LTS

# ---------------------------------------------------------------- Python + requirements.txt
PY=""
for c in python3 python py; do have "$c" && PY=$c && break; done
if [ -z "$PY" ]; then
  install_pkg python3 python@3.12 Python.Python.3.12 && PY=python3
fi
if [ -n "$PY" ]; then
  say "Instalando requirements.txt ($PY -m pip)"
  "$PY" -m pip install --user -r requirements.txt \
    || "$PY" -m pip install --user --break-system-packages -r requirements.txt \
    || warn "No se pudo ejecutar pip. Prueba: $PY -m pip install -r requirements.txt"
fi

# ---------------------------------------------------------------- ffmpeg
if ! have ffmpeg; then
  install_pkg ffmpeg ffmpeg Gyan.FFmpeg
fi
if ! have ffmpeg && [ -n "$PY" ]; then
  # Plan B: el ffmpeg portátil de pip, copiado a la carpeta de datos de la app (ahí lo busca Cyberia primero).
  case "$OS" in
    mac) BIN="$HOME/Library/Application Support/Cyberia/bin" ;;
    win) BIN="${APPDATA:-$HOME/AppData/Roaming}/Cyberia/bin" ;;
    *) BIN="${XDG_CONFIG_HOME:-$HOME/.config}/Cyberia/bin" ;;
  esac
  say "Copiando ffmpeg portátil a: $BIN"
  "$PY" - "$BIN" <<'PYEOF' || warn "No se pudo preparar ffmpeg. Instálalo a mano (brew install ffmpeg / winget install Gyan.FFmpeg)."
import os, shutil, stat, sys
import imageio_ffmpeg
dst_dir = sys.argv[1]
os.makedirs(dst_dir, exist_ok=True)
dst = os.path.join(dst_dir, "ffmpeg.exe" if os.name == "nt" else "ffmpeg")
shutil.copy2(imageio_ffmpeg.get_ffmpeg_exe(), dst)
os.chmod(dst, os.stat(dst).st_mode | stat.S_IEXEC)
print("ok:", dst)
PYEOF
fi

# ---------------------------------------------------------------- Wails + proyecto
if have go; then
  say "Instalando la CLI de Wails"
  go install github.com/wailsapp/wails/v2/cmd/wails@v2.10.2
  GOBIN_DIR="$(go env GOPATH)/bin"
  case ":$PATH:" in *":$GOBIN_DIR:"*) ;; *) warn "Agrega $GOBIN_DIR a tu PATH para usar el comando 'wails'." ;; esac
  say "Descargando dependencias de Go"
  go mod tidy
fi
if have npm; then
  say "Instalando dependencias de la interfaz (src/frontend)"
  # Sin lockfile ni node_modules viejos: evita el error de npm con los módulos nativos de cada sistema (rollup/esbuild).
  rm -rf src/frontend/node_modules src/frontend/package-lock.json
  npm install --prefix src/frontend --no-audit --no-fund
fi

say "Listo"
[ "$NEED_RESTART" = 1 ] && warn "Cierra y vuelve a abrir Git Bash para que reconozca lo que se instaló, y ejecuta este script otra vez si algo faltó."
echo "Siguiente paso:  ./scripts/dev.sh  (probar)   o   ./scripts/build.sh  (compilar la app)"
