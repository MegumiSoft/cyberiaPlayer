// Análisis offline de una canción. La decodificación ocurre aquí (la API de audio solo existe en el hilo
// principal, pero decodifica fuera de él); los cálculos pesados corren en un Worker para no trabar la interfaz.
import { api } from '../api'
import { b64ToBytes } from './bytes'
import { ANALYSIS_SR, analyzeMono, type Analysis } from './analysisCore'

export type { Analysis, KeyResult } from './analysisCore'
export { camelot, keyName, keyText } from './analysisCore'

/** Debe coincidir con trackmeta.AnalysisVersion del backend. */
export const ANALYSIS_VERSION = 3

/** Por encima de este tamaño el webview no decodifica el archivo él mismo (ocuparía gigas de RAM): lo hace el backend. */
export const MAX_ANALYZE_BYTES = 24 * 1024 * 1024

/** Los formatos sin pérdida (WAV, FLAC, AIFF) y los archivos grandes se decodifican en el backend con ffmpeg. */
const BACKEND_EXT = ['wav', 'flac', 'aif', 'aiff']
export const needsBackendDecode = (t: { ext: string; size: number }): boolean => t.size > MAX_ANALYZE_BYTES || BACKEND_EXT.includes(t.ext.toLowerCase())

let worker: Worker | null = null
let workerBroken = false
let nextId = 0
const pending = new Map<number, { resolve: (a: Analysis) => void; reject: (e: Error) => void }>()

function failPending(err: Error): void {
  for (const p of pending.values()) p.reject(err)
  pending.clear()
}

function getWorker(): Worker | null {
  if (workerBroken) return null
  if (worker) return worker
  try {
    const w = new Worker(new URL('./analysis.worker.ts', import.meta.url), { type: 'module' })
    // Un solo oyente para todas las peticiones: cada respuesta trae el id de la suya.
    w.addEventListener('message', (e: MessageEvent<{ id: number; ok: boolean; res?: Analysis; error?: string }>) => {
      const p = pending.get(e.data.id)
      if (!p) return
      pending.delete(e.data.id)
      if (e.data.ok) p.resolve(e.data.res as Analysis)
      else p.reject(new Error(e.data.error))
    })
    w.addEventListener('error', () => {
      // El Worker no arrancó (p. ej. webview sin módulos): los pendientes fallan y los siguientes se calculan en el hilo principal.
      workerBroken = true
      worker = null
      failPending(new Error('el Worker de análisis no está disponible'))
    })
    worker = w
    return w
  } catch {
    workerBroken = true
    return null
  }
}

function runInWorker(mono: Float32Array): Promise<Analysis> {
  const w = getWorker()
  if (!w) return Promise.resolve().then(() => analyzeMono(mono, ANALYSIS_SR))
  return new Promise((resolve, reject) => {
    const id = ++nextId
    pending.set(id, { resolve, reject })
    w.postMessage({ id, mono, sr: ANALYSIS_SR }, [mono.buffer])
  })
}

export async function analyze(data: ArrayBuffer): Promise<Analysis> {
  const AC = window.OfflineAudioContext ?? (window as unknown as { webkitOfflineAudioContext: typeof OfflineAudioContext }).webkitOfflineAudioContext
  const ac = new AC(1, 1, ANALYSIS_SR)
  // decodeAudioData consume (desvincula) el buffer: se le pasa una copia para no perder el original de la caché.
  const buf = await ac.decodeAudioData(data.slice(0))
  const n = buf.length
  const mono = new Float32Array(n)
  const chans = Array.from({ length: buf.numberOfChannels }, (_, i) => buf.getChannelData(i))
  for (let c = 0; c < chans.length; c++) {
    const ch = chans[c]
    for (let i = 0; i < n; i++) mono[i] += ch[i]
  }
  if (chans.length > 1) for (let i = 0; i < n; i++) mono[i] /= chans.length
  return runInWorker(mono)
}

/** La interfaz ya no necesita este análisis (se saltó a otra canción o se detuvo el lote). */
export class StaleAnalysis extends Error {
  constructor() {
    super('análisis descartado')
  }
}

let handleSeq = 0
const newHandle = (): string => `a${++handleSeq}-${Date.now().toString(36)}`

/** El backend rechaza lo que dura más de 30 min (decode.ErrTooLong); este fragmento de su mensaje lo identifica. */
export const isTooLongError = (e: unknown): boolean => String((e as { message?: string })?.message ?? e).includes('más de 30 minutos')

/**
 * Analiza una canción que el backend decodifica con ffmpeg (PCM mono de 16 bits) y entrega por trozos.
 * `isStale` (opcional) se consulta mientras ffmpeg decodifica y entre trozos: si devuelve true se cancela ffmpeg, se
 * libera el PCM y se lanza StaleAnalysis (así saltar rápido entre canciones largas no deja decodificaciones corriendo).
 */
export async function analyzeViaBackend(path: string, isStale?: () => boolean): Promise<Analysis> {
  const handle = newHandle()
  const poll = isStale
    ? setInterval(() => {
        if (isStale()) void api.cancelAnalysis(handle).catch(() => undefined)
      }, 250)
    : undefined
  try {
    const total = await api.prepareAnalysis(handle, path).catch((e) => {
      if (isStale?.()) throw new StaleAnalysis()
      throw e
    })
    const mono = new Float32Array(Math.floor(total / 2))
    let i = 0
    const CH = 4 * 1024 * 1024
    for (let off = 0; off < total; off += CH) {
      if (isStale?.()) throw new StaleAnalysis()
      const b = b64ToBytes(await api.readAnalysisChunk(handle, off, Math.min(CH, total - off)))
      const dv = new DataView(b.buffer, b.byteOffset, b.byteLength)
      for (let k = 0; k + 1 < b.byteLength && i < mono.length; k += 2) mono[i++] = dv.getInt16(k, true) / 32768
    }
    if (isStale?.()) throw new StaleAnalysis()
    return await runInWorker(i === mono.length ? mono : mono.slice(0, i))
  } finally {
    if (poll) clearInterval(poll)
    void api.releaseAnalysis(handle).catch(() => undefined)
  }
}
