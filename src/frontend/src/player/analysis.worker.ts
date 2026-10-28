import { analyzeMono } from './analysisCore'

// Cada petición lleva un `id` que se devuelve en la respuesta: si dos análisis se solapan, cada uno recibe el suyo.
self.onmessage = (e: MessageEvent<{ id: number; mono: Float32Array; sr: number }>) => {
  const { id } = e.data
  try {
    const res = analyzeMono(e.data.mono, e.data.sr)
    ;(self as unknown as Worker).postMessage({ id, ok: true, res }, [res.wave.buffer])
  } catch (err) {
    ;(self as unknown as Worker).postMessage({ id, ok: false, error: String(err) })
  }
}
