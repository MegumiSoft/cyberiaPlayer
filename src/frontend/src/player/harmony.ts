// Compatibilidad armónica y de tempo entre canciones (rueda Camelot).
import { camelot, type KeyResult } from './analysisCore'

export type Relation = 'same' | 'adjacent' | 'relative' | 'boost' | null

export const RELATION_LABEL: Record<Exclude<Relation, null>, string> = {
  same: 'MISMA TONALIDAD',
  adjacent: '±1 EN LA RUEDA',
  relative: 'RELATIVA (A↔B)',
  boost: '±2 EN LA RUEDA',
}

/** Número (1-12) y letra de la rueda Camelot. */
export function wheel(k: KeyResult): { n: number; letter: 'A' | 'B' } {
  const c = camelot(k)
  return { n: parseInt(c, 10), letter: c.endsWith('A') ? 'A' : 'B' }
}

const dist = (a: number, b: number) => {
  const d = Math.abs(a - b) % 12
  return Math.min(d, 12 - d)
}

/** Cómo se relaciona `b` con `a` al mezclar (null si chocan). */
export function relation(a: KeyResult, b: KeyResult): Relation {
  const wa = wheel(a)
  const wb = wheel(b)
  if (wa.n === wb.n && wa.letter === wb.letter) return 'same'
  if (wa.n === wb.n) return 'relative'
  if (wa.letter === wb.letter) {
    const d = dist(wa.n, wb.n)
    if (d === 1) return 'adjacent'
    if (d === 2) return 'boost'
  }
  return null
}

/** Diferencia de BPM en %, probando también el doble y la mitad (un tema a 70 mezcla con otro a 140). */
export function bpmDelta(a: number, b: number): { pct: number; factor: 1 | 2 | 0.5 } {
  let best = { pct: ((b - a) / a) * 100, factor: 1 as 1 | 2 | 0.5 }
  for (const f of [2, 0.5] as const) {
    const pct = ((b * f - a) / a) * 100
    if (Math.abs(pct) < Math.abs(best.pct) - 0.01) best = { pct, factor: f }
  }
  return best
}

const RELATION_SCORE: Record<Exclude<Relation, null>, number> = { same: 3, adjacent: 2, relative: 2, boost: 1 }

/** Puntaje de compatibilidad (mayor = mejor); 0 si no mezcla dentro de la tolerancia de BPM. */
export function compatScore(
  a: { bpm: number; key: KeyResult | null },
  b: { bpm: number; key: KeyResult | null },
  tolerancePct: number,
): { score: number; rel: Relation; pct: number; factor: 1 | 2 | 0.5 } {
  if (!a.key || !b.key || a.bpm <= 0 || b.bpm <= 0) return { score: 0, rel: null, pct: 0, factor: 1 }
  const rel = relation(a.key, b.key)
  const d = bpmDelta(a.bpm, b.bpm)
  if (!rel || Math.abs(d.pct) > tolerancePct) return { score: 0, rel, pct: d.pct, factor: d.factor }
  // más puntos por mejor armonía; dentro de lo mismo, gana el BPM más cercano
  return { score: RELATION_SCORE[rel] * 100 - Math.abs(d.pct) * 5 - (d.factor === 1 ? 0 : 10), rel, pct: d.pct, factor: d.factor }
}
