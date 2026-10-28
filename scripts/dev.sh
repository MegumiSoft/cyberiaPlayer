#!/usr/bin/env bash
# Abre la app en modo desarrollo (recarga en vivo).
cd "$(dirname "$0")/.." && exec wails dev
