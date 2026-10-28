// Análisis de toda la biblioteca en segundo plano (alimenta el mapa de canciones y, más adelante, la exportación).
import { reactive } from 'vue'
import { api } from '../api'
import type { LibTrack } from '../types'
import { analyze, analyzeViaBackend, ANALYSIS_VERSION, needsBackendDecode, type Analysis } from './analysis'
import { b64ToBytes, bytesToB64 } from './bytes'
import { metaOf, updateMeta } from './trackmeta'

export const batch = reactive({ running: false, done: 0, total: 0, current: '', failed: 0, cancel: false })

/** Guarda el análisis (números en el JSON de metadatos, onda en archivo aparte). */
export function persistAnalysis(track: LibTrack, a: Analysis): void {
  updateMeta(track.path, (m) => {
    m.analysis = {
      size: track.size, version: ANALYSIS_VERSION, duration: a.duration, bpm: a.bpm, bpmConf: a.bpmConf, bpmAlt: a.bpmAlt, beatOffset: a.beatOffset,
      keyPc: a.key?.pc ?? 0, keyMinor: a.key?.minor ?? false, keyConf: a.key?.conf ?? 0, hasKey: !!a.key,
    }
  })
  void api.saveWave(track.path, track.size, bytesToB64(a.wave)).catch(() => undefined)
}

export const isAnalyzed = (t: LibTrack): boolean => {
  const a = metaOf(t.path).analysis
  return !!a && a.version === ANALYSIS_VERSION && a.size === t.size
}

/** Pendientes: las que no tienen un análisis vigente. */
export const pendingTracks = (tracks: LibTrack[]): LibTrack[] => tracks.filter((t) => !isAnalyzed(t))

const CHUNK = 1 << 20
async function readWhole(track: LibTrack): Promise<ArrayBuffer> {
  const out = new Uint8Array(track.size)
  for (let off = 0; off < track.size; off += CHUNK) {
    const b = b64ToBytes(await api.readTrackChunk(track.path, off, Math.min(CHUNK, track.size - off)))
    out.set(b, off)
  }
  return out.buffer
}

/** Analiza de a una canción (el cálculo pesado corre en un Worker) y guarda cada resultado al terminar. */
export async function analyzeAll(tracks: LibTrack[]): Promise<void> {
  if (batch.running) return
  const todo = pendingTracks(tracks)
  batch.running = true
  batch.cancel = false
  batch.done = 0
  batch.failed = 0
  batch.total = todo.length
  try {
    for (const t of todo) {
      if (batch.cancel) break
      batch.current = t.title
      try {
        persistAnalysis(t, needsBackendDecode(t) ? await analyzeViaBackend(t.path) : await analyze(await readWhole(t)))
      } catch {
        batch.failed++
      }
      batch.done++
      await new Promise((r) => setTimeout(r, 150)) // deja respirar a la interfaz y al reproductor
    }
  } finally {
    batch.running = false
    batch.current = ''
  }
}
