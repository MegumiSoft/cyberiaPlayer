// Paleta del skin: cian -> azul -> violeta -> magenta -> naranja -> amarillo.
export type RGB = [number, number, number]

const STOPS: RGB[] = [
  [24, 224, 255],
  [58, 120, 255],
  [150, 70, 255],
  [255, 47, 176],
  [255, 122, 69],
  [255, 230, 0],
]

/** Color en x ∈ [0,1] (se recorta fuera de ese rango). */
export function pal(x: number): RGB {
  const t = Math.min(1, Math.max(0, x)) * (STOPS.length - 1)
  const i = Math.min(STOPS.length - 2, Math.floor(t))
  const f = t - i
  const a = STOPS[i]
  const b = STOPS[i + 1]
  return [a[0] + (b[0] - a[0]) * f, a[1] + (b[1] - a[1]) * f, a[2] + (b[2] - a[2]) * f]
}

/** Como pal(), pero cíclico (ida y vuelta): sirve para animar el color con el tiempo sin saltos. */
export function palWrap(x: number): RGB {
  let t = x % 2
  if (t < 0) t += 2
  return pal(t > 1 ? 2 - t : t)
}

export function rgba(c: RGB, a: number): string {
  return `rgba(${c[0] | 0},${c[1] | 0},${c[2] | 0},${a})`
}
