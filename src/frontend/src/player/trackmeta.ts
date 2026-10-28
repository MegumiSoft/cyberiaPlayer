// Metadatos por canción en el front: análisis guardado + rejilla y cues editados por el usuario.
import { reactive } from 'vue'
import { api } from '../api'
import type { Cue, TrackMeta } from '../types'

export const CUE_COLORS = ['#18e0ff', '#3a78ff', '#9646ff', '#ff2fb0', '#ff7a45', '#ffe600', '#3dff9a', '#ffffff']
export const HOT_LETTERS = 'ABCDEFGH'

export const metas = reactive<Record<string, TrackMeta>>({})

export const blankMeta = (): TrackMeta => ({ analysis: null, gridSet: false, gridBpm: 0, gridOffset: 0, cues: [] })

export async function loadMetas(): Promise<void> {
  try {
    const all = await api.trackMeta()
    for (const k of Object.keys(metas)) delete metas[k]
    for (const [k, v] of Object.entries(all ?? {})) metas[k] = { ...blankMeta(), ...v, cues: v.cues ?? [] }
  } catch {
    /* sin metadatos guardados: se empieza de cero */
  }
}

export const metaOf = (path: string): TrackMeta => metas[path] ?? blankMeta()

const timers = new Map<string, ReturnType<typeof setTimeout>>()

/** Cambia los metadatos de una canción y los guarda (agrupando ráfagas de cambios). */
export function updateMeta(path: string, fn: (m: TrackMeta) => void): void {
  const m = metas[path] ?? (metas[path] = blankMeta())
  fn(m)
  clearTimeout(timers.get(path))
  timers.set(
    path,
    setTimeout(() => {
      timers.delete(path)
      void api.saveTrackMeta(path, JSON.parse(JSON.stringify(m))).catch(() => undefined)
    }, 400),
  )
}

/** Cuando una canción cambia de playlist, sus metadatos viajan con ella. */
export function renameMeta(from: string, to: string): void {
  if (metas[from]) {
    metas[to] = metas[from]
    delete metas[from]
  }
}

/** Una copia de la canción en otra playlist hereda los metadatos del original. */
export function copyMeta(from: string, to: string): void {
  if (metas[from]) metas[to] = JSON.parse(JSON.stringify(metas[from]))
}

export interface Grid {
  bpm: number
  period: number
  offset: number
  edited: boolean
}

/** Rejilla efectiva: la editada a mano manda sobre la detectada. */
export function gridOf(path: string | undefined): Grid | null {
  if (!path) return null
  const m = metas[path]
  if (!m) return null
  if (m.gridSet && m.gridBpm > 0) return { bpm: m.gridBpm, period: 60 / m.gridBpm, offset: m.gridOffset, edited: true }
  const a = m.analysis
  if (a && a.bpm > 0) return { bpm: a.bpm, period: 60 / a.bpm, offset: a.beatOffset, edited: false }
  return null
}

/** Acerca un tiempo (s) al pulso más cercano de la rejilla. */
export function snapToGrid(g: Grid | null, t: number): number {
  if (!g) return t
  const k = Math.round((t - g.offset) / g.period)
  const bt = g.offset + k * g.period
  return bt < 0 ? g.offset + Math.ceil(-g.offset / g.period) * g.period : bt
}

export const sortedCues = (m: TrackMeta): Cue[] => [...m.cues].sort((a, b) => a.timeMs - b.timeMs)
