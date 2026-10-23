import { analyzeMono } from './analysisCore'

self.onmessage = (e: MessageEvent<{ mono: Float32Array; sr: number }>) => {
  try {
    const res = analyzeMono(e.data.mono, e.data.sr)
    ;(self as unknown as Worker).postMessage({ ok: true, res }, [res.wave.buffer])
  } catch (err) {
    ;(self as unknown as Worker).postMessage({ ok: false, error: String(err) })
  }
}
