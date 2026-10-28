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

function getWorker(): Worker | null {
  if (workerBroken) return null
  if (worker) return worker
  try {
    worker = new Worker(new URL('./analysis.worker.ts', import.meta.url), { type: 'module' })
    worker.addEventListener('error', () => {
      workerBroken = true
      worker = null
    })
    return worker
  } catch {
    workerBroken = true
    return null
  }
}

function runInWorker(mono: Float32Array): Promise<Analysis> {
  const w = getWorker()
  if (!w) return Promise.resolve().then(() => analyzeMono(mono, ANALYSIS_SR))
  return new Promise((resolve, reject) => {
    const onMsg = (e: MessageEvent) => {
      w.removeEventListener('message', onMsg)
      w.removeEventListener('error', onErr)
      if (e.data.ok) resolve(e.data.res as Analysis)
      else reject(new Error(e.data.error))
    }
    const onErr = () => {
      w.removeEventListener('message', onMsg)
      w.removeEventListener('error', onErr)
      // el Worker no arrancó (p. ej. webview sin módulos): se calcula en el hilo principal
      resolve(analyzeMono(mono, ANALYSIS_SR))
    }
    w.addEventListener('message', onMsg)
    w.addEventListener('error', onErr)
    w.postMessage({ mono, sr: ANALYSIS_SR }, [mono.buffer])
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

/** Analiza una canción que el backend decodifica con ffmpeg (PCM mono de 16 bits) y entrega por trozos. */
export async function analyzeViaBackend(path: string): Promise<Analysis> {
  const total = await api.prepareAnalysis(path)
  try {
    const mono = new Float32Array(Math.floor(total / 2))
    let i = 0
    const CH = 4 * 1024 * 1024
    for (let off = 0; off < total; off += CH) {
      const b = b64ToBytes(await api.readAnalysisChunk(path, off, Math.min(CH, total - off)))
      const dv = new DataView(b.buffer, b.byteOffset, b.byteLength)
      for (let k = 0; k + 1 < b.byteLength && i < mono.length; k += 2) mono[i++] = dv.getInt16(k, true) / 32768
    }
    return await runInWorker(i === mono.length ? mono : mono.slice(0, i))
  } finally {
    void api.releaseAnalysis(path).catch(() => undefined)
  }
}
