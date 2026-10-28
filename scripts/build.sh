#!/usr/bin/env bash
# Compila la app para tu sistema: build/bin/Cyberia.app (macOS) o build/bin/Cyberia.exe (Windows).
#
# Uso:  ./scripts/build.sh                 compila para este equipo
#       ./scripts/build.sh --universal     (macOS) un .app que corre en Intel y Apple Silicon
#       ./scripts/build.sh --zip           además deja el .zip listo para compartir en dist/
#       (se pueden combinar: ./scripts/build.sh --universal --zip)
set -e
cd "$(dirname "$0")/.."

UNIVERSAL=0
ZIP=0
for arg in "$@"; do
  case "$arg" in
    --universal) UNIVERSAL=1 ;;
    --zip) ZIP=1 ;;
    *) echo "Opción desconocida: $arg"; exit 2 ;;
  esac
done
command -v wails >/dev/null 2>&1 || { echo "No encuentro 'wails'. Ejecuta ./scripts/setup.sh (y agrega \$(go env GOPATH)/bin a tu PATH)."; exit 1; }

mkdir -p src/frontend/dist && touch src/frontend/dist/.gitkeep

if [ "$(uname -s)" = "Darwin" ]; then
  # Causa típica de «Cyberia está dañada o incompleta»: un Info.plist viejo de cuando el proyecto se llamaba «musicdl».
  rm -f build/darwin/Info.plist
  PLATFORM=()
  [ "$UNIVERSAL" = 1 ] && PLATFORM=(-platform darwin/universal)
  wails build -clean -skipembedcreate "${PLATFORM[@]}"

  APP="build/bin/Cyberia.app"
  [ -d "$APP" ] || { echo "No se generó $APP"; exit 1; }
  # El ejecutable declarado en Info.plist debe existir dentro del .app.
  EXE=$(/usr/libexec/PlistBuddy -c 'Print :CFBundleExecutable' "$APP/Contents/Info.plist")
  [ -f "$APP/Contents/MacOS/$EXE" ] || { echo "Info.plist pide '$EXE' pero en Contents/MacOS hay: $(ls "$APP/Contents/MacOS")"; exit 1; }

  # Firma ad-hoc de TODO el paquete (sin ella macOS en Apple Silicon dice «está dañada») y sin cuarentena.
  xattr -cr "$APP"
  codesign --force --deep --sign - "$APP"
  codesign --verify --deep --strict "$APP"
  echo "Arquitecturas: $(lipo -archs "$APP/Contents/MacOS/$EXE")"
  echo "OK: $APP lista. Ábrela con doble clic."

  if [ "$ZIP" = 1 ]; then
    mkdir -p dist
    rm -f dist/Cyberia-macos.zip
    # ditto conserva la firma y los atributos del .app (un zip normal puede romperla).
    ditto -c -k --sequesterRsrc --keepParent "$APP" dist/Cyberia-macos.zip
    echo "Zip para compartir: dist/Cyberia-macos.zip"
    echo "Quien lo descargue debe: descomprimir, mover Cyberia.app a Aplicaciones y ejecutar una vez:"
    echo "    xattr -cr /Applications/Cyberia.app"
    echo "(o: Ajustes del Sistema → Privacidad y seguridad → «Abrir de todos modos»)"
  fi
else
  wails build -clean -skipembedcreate
  echo "OK: la app quedó en build/bin/"
  if [ "$ZIP" = 1 ]; then
    mkdir -p dist
    EXE_FILE=$(ls build/bin/*.exe | head -1)
    ( cd build/bin && powershell -NoProfile -Command "Compress-Archive -Force -Path '$(basename "$EXE_FILE")' -DestinationPath '../../dist/Cyberia-windows.zip'" 2>/dev/null ) \
      || ( cd build/bin && zip -q ../../dist/Cyberia-windows.zip "$(basename "$EXE_FILE")" )
    echo "Zip para compartir: dist/Cyberia-windows.zip"
  fi
fi
