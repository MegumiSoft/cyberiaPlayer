// Análisis offline de una canción. La decodificación ocurre aquí (la API de audio solo existe en el hilo
// principal, pero decodifica fuera de él); los cálculos pesados corren en un Worker para no trabar la interfaz.
import { ANALYSIS_SR, analyzeMono, type Analysis } from './analysisCore'

export type { Analysis, KeyResult } from './analysisCore'
export { camelot, keyName, keyText } from './analysisCore'

/** Debe coincidir con trackmeta.AnalysisVersion del backend. */
export const ANALYSIS_VERSION = 2

/** Por encima de este tamaño no se analiza (decodificar un mix de una hora ocuparía gigas de RAM). */
export const MAX_ANALYZE_BYTES = 24 * 1024 * 1024

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
