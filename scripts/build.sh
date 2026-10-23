#!/usr/bin/env bash
# Compila la app para tu sistema: build/bin/Cyberia.app (macOS) o build/bin/Cyberia.exe (Windows).
# Uso:  ./scripts/build.sh
set -e
cd "$(dirname "$0")/.."

if [ "$(uname -s)" = "Darwin" ]; then
  # Causa típica de «Cyberia está dañada o incompleta»: un Info.plist viejo de cuando el proyecto se llamaba «musicdl».
  rm -f build/darwin/Info.plist build/darwin/Info.dev.plist
  wails build -clean -skipembedcreate
  APP="build/bin/Cyberia.app"
  [ -d "$APP" ] || { echo "No se generó $APP"; exit 1; }
  # El ejecutable declarado en Info.plist debe existir dentro del .app.
  EXE=$(/usr/libexec/PlistBuddy -c 'Print :CFBundleExecutable' "$APP/Contents/Info.plist")
  [ -f "$APP/Contents/MacOS/$EXE" ] || { echo "Info.plist pide '$EXE' pero en Contents/MacOS hay: $(ls "$APP/Contents/MacOS")"; exit 1; }
  xattr -cr "$APP"                         # quita la marca de cuarentena
  codesign --force --deep --sign - "$APP"  # firma ad-hoc: sin ella macOS arm64 puede decir «dañada»
  codesign --verify --deep --strict "$APP" && echo "OK: $APP lista. Ábrela con doble clic."
else
  wails build -clean -skipembedcreate
  echo "OK: la app quedó en build/bin/"
fi
