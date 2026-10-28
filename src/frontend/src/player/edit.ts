// Acciones de edición sobre la canción que suena: rejilla de pulsos y cues.
import { reactive } from 'vue'
import { current, player, playbackTime, seek, syncGrid } from './engine'
import { blankMeta, gridOf, metaOf, snapToGrid, updateMeta } from './trackmeta'

export const edit = reactive({ snap: true, onset: true })

const path = (): string | undefined => current()?.track.path

/**
 * Lleva el desfase de la rejilla a [0, 4 pulsos): el pulso del desfase es SIEMPRE un "1" de compás, así que
 * restar compases enteros mantiene la fase (qué pulso es el 1). Restar un solo pulso la perdería.
 */
function reduce(offset: number, period: number): number {
  const bar = period * 4
  const o = offset - Math.floor(offset / bar) * bar
  return Math.round(o * 10000) / 10000
}

/** Afina un instante al golpe (subida de energía en graves/medios) más cercano, hasta ±90 ms. */
function toOnset(t: number): number {
  const a = player.analysis
  if (!a || !a.wave.length || !a.fps) return t
  const e = (f: number): number => 2 * a.wave[f * 3] + a.wave[f * 3 + 1]
  const c = Math.round(t * a.fps)
  const r = Math.round(0.09 * a.fps)
  let best = -1
  let bestD = 0
  for (let f = Math.max(1, c - r); f <= Math.min(a.frames - 1, c + r); f++) {
    const d = e(f) - e(f - 1)
    if (d > bestD) {
      bestD = d
      best = f
    }
  }
  return best >= 0 && bestD >= 90 ? best / a.fps : t
}

// ---------------------------------------------------------------- rejilla
/** Cambia el BPM manteniendo fijo el pulso más cercano al cabezal (la rejilla se "gira" alrededor de donde escuchas). */
export function setGridBpm(bpm: number): void {
  const p = path()
  if (!p || !(bpm >= 40 && bpm <= 300)) return
  const g = gridOf(p)
  const t = playbackTime()
  updateMeta(p, (m) => {
    let offset: number
    if (g) {
      const k = Math.round((t - g.offset) / g.period)
      const anchor = g.offset + k * g.period
      offset = reduce(anchor - k * (60 / bpm), 60 / bpm)
    } else {
      offset = reduce(t, 60 / bpm)
    }
    m.gridSet = true
    m.gridBpm = Math.round(bpm * 100) / 100
    m.gridOffset = offset
  })
  syncGrid()
}

export const scaleGridBpm = (factor: number): void => {
  const g = gridOf(path())
  if (g) setGridBpm(g.bpm * factor)
}

export function nudgeGrid(deltaSec: number): void {
  const p = path()
  const g = gridOf(p)
  if (!p || !g) return
  updateMeta(p, (m) => {
    m.gridSet = true
    m.gridBpm = g.bpm
    m.gridOffset = reduce(g.offset + deltaSec, g.period)
  })
}

/**
 * Fija el pulso 1 en el cabezal: ese punto pasa a ser un tiempo fuerte (inicio de compás/frase) y toda la
 * rejilla, antes y después, se acomoda a partir de él con el BPM actual.
 */
export function firstBeatHere(): void {
  const p = path()
  const g = gridOf(p)
  if (!p || !g) return
  const t = edit.onset ? toOnset(playbackTime()) : playbackTime()
  updateMeta(p, (m) => {
    m.gridSet = true
    m.gridBpm = g.bpm
    m.gridOffset = reduce(t, g.period)
  })
}

/** Mueve cuál es el pulso 1 sin mover la rejilla (±1 pulso): útil si el golpe fuerte cae en el 2, 3 o 4. */
export function shiftDownbeat(beats: number): void {
  const p = path()
  const g = gridOf(p)
  if (!p || !g) return
  updateMeta(p, (m) => {
    m.gridSet = true
    m.gridBpm = g.bpm
    m.gridOffset = reduce(g.offset + beats * g.period, g.period)
  })
}

export function resetGrid(): void {
  const p = path()
  if (!p) return
  updateMeta(p, (m) => {
    m.gridSet = false
    m.gridBpm = 0
    m.gridOffset = 0
  })
  syncGrid()
}

// ---------------------------------------------------------------- cues
const quantized = (t: number): number => (edit.snap ? snapToGrid(gridOf(path()), t) : t)

/** Hot cue A-H: si está vacío se crea en la posición actual; si existe, salta a él. */
export function pressHotCue(slot: number): void {
  const p = path()
  if (!p || slot < 0 || slot > 7) return
  const existing = metaOf(p).cues.find((c) => c.slot === slot)
  if (existing) {
    seek(existing.timeMs / 1000)
    return
  }
  const timeMs = Math.round(quantized(playbackTime()) * 1000)
  updateMeta(p, (m) => {
    m.cues.push({ slot, timeMs, name: '', color: slot })
  })
}

export function deleteHotCue(slot: number): void {
  const p = path()
  if (!p) return
  updateMeta(p, (m) => {
    m.cues = m.cues.filter((c) => c.slot !== slot)
  })
}

export function renameHotCue(slot: number, name: string): void {
  const p = path()
  if (!p) return
  updateMeta(p, (m) => {
    const c = m.cues.find((x) => x.slot === slot)
    if (c) c.name = name.slice(0, 32)
  })
}

export function addMemoryCue(): void {
  const p = path()
  if (!p) return
  const timeMs = Math.round(quantized(playbackTime()) * 1000)
  updateMeta(p, (m) => {
    if (m.cues.some((c) => c.slot === -1 && Math.abs(c.timeMs - timeMs) < 50)) return
    if (m.cues.length >= 40) return
    m.cues.push({ slot: -1, timeMs, name: '', color: 0 })
  })
}

export function deleteMemoryCue(timeMs: number): void {
  const p = path()
  if (!p) return
  updateMeta(p, (m) => {
    m.cues = m.cues.filter((c) => !(c.slot === -1 && c.timeMs === timeMs))
  })
}

export { blankMeta }
