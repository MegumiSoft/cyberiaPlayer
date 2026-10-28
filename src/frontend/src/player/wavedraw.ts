// Dibujo de la forma de onda en 3 bandas (estilo Rekordbox) con la paleta neón del skin.
import type { Analysis } from './analysis'
import { rgba, type RGB } from './palette'

// Cada banda se pinta con un degradado vertical (del borde al centro), de la más ancha (graves) a la más fina (agudos):
// cada capa queda visible sobre la anterior y los degradados dan el aspecto "vaporwave".
const LOW: [RGB, RGB] = [[58, 120, 255], [150, 70, 255]] // azul -> violeta
const MID: [RGB, RGB] = [[255, 47, 176], [255, 122, 69]] // magenta -> naranja
const HIGH: [RGB, RGB] = [[24, 224, 255], [215, 255, 255]] // cian -> blanco frío
const LANES = [
  { colors: LOW, scale: 0.98, alpha: 0.95 },
  { colors: MID, scale: 0.8, alpha: 0.85 },
  { colors: HIGH, scale: 0.55, alpha: 0.9 },
] as const

function gradient(ctx: CanvasRenderingContext2D, cy: number, half: number, [edge, center]: [RGB, RGB], alpha: number) {
  const g = ctx.createLinearGradient(0, cy - half, 0, cy + half)
  g.addColorStop(0, rgba(edge, alpha))
  g.addColorStop(0.5, rgba(center, alpha))
  g.addColorStop(1, rgba(edge, alpha))
  return g
}

/**
 * Pinta columnas de forma de onda. `sample(x)` devuelve el rango de cuadros [a, b) de la columna x.
 * Las barras se espejan sobre el centro vertical.
 */
export function drawBands(
  ctx: CanvasRenderingContext2D,
  an: Analysis,
  w: number,
  h: number,
  range: (x: number) => [number, number],
  colW = 1,
  top = 0,
): void {
  const cy = top + (h - top) / 2
  const half = (h - top) / 2 - 1
  const data = an.wave
  const prev = ctx.globalCompositeOperation
  ctx.globalCompositeOperation = 'source-over'
  for (let lane = 0; lane < 3; lane++) {
    const L = LANES[lane]
    ctx.fillStyle = gradient(ctx, cy, half, L.colors as [RGB, RGB], L.alpha)
    ctx.beginPath()
    for (let x = 0; x < w; x += colW) {
      const [a, b] = range(x)
      let v = 0
      const lo = Math.max(0, a)
      const hi = Math.min(an.frames, Math.max(b, a + 1))
      for (let f = lo; f < hi; f++) {
        const d = data[f * 3 + lane]
        if (d > v) v = d
      }
      const hh = (v / 255) * half * L.scale
      if (hh > 0.4) ctx.rect(x, cy - hh, colW, hh * 2)
    }
    ctx.fill()
  }
  ctx.globalCompositeOperation = prev
}
