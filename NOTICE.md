# Licencia y créditos

Este programa se distribuye bajo la **GNU General Public License v2 o posterior (GPL-2.0-or-later)**; el texto está en `LICENSE`.

El paquete `src/backend/pioneer` (exportación a USB para equipos Pioneer) es un port a Go de la lógica de
[rbxport](https://github.com/chrisle/rbxport) © 2026 Chris Le y colaboradores, también GPL-2.0-or-later
(crates `rbl-pdb`, `rbl-anlz` y `rbl-export`: formato DeviceSQL/ANLZ, filas constantes, paleta de colores
de cues y fórmula de la carpeta de análisis). Por eso todo el proyecto queda bajo GPL: si lo compartes,
compártelo con su código fuente y esta misma licencia.

Las ideas de detección de tonalidad (perfiles EDMA de Faraldo, afinación, armónicos) están inspiradas en esa
misma investigación; el código es propio.
