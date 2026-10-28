import { describe, expect, it } from 'vitest'
import { analyzeMono, magnitudeSpectrum } from './analysisCore'

// Pistas sintéticas a tempo conocido (bombo, caja, hi-hats, bajo) para fijar el detector de BPM: si alguien cambia el
// algoritmo y un género deja de salir bien, estos tests lo avisan. Todas empiezan con un bombo en t = 0.
const SR = 22050

function rng(seed: number): () => number {
  let a = seed >>> 0
  return () => {
    a = (a + 0x6d2b79f5) >>> 0
    let t = a
    t = Math.imul(t ^ (t >>> 15), t | 1)
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61)
    return (((t ^ (t >>> 14)) >>> 0) / 4294967296) * 2 - 1
  }
}

const rand = rng(1)
const kick = (() => {
  const n = Math.floor(0.25 * SR)
  const out = new Float32Array(n)
  let ph = 0
  for (let i = 0; i < n; i++) {
    const t = i / SR
    ph += (2 * Math.PI * (45 + 90 * Math.exp(-t * 30))) / SR
    out[i] = Math.sin(ph) * Math.exp(-t * 12)
  }
  return out
})()
const snare = (() => {
  const n = Math.floor(0.18 * SR)
  const out = new Float32Array(n)
  for (let i = 0; i < n; i++) {
    const t = i / SR
    out[i] = (rand() * 0.6 + Math.sin(2 * Math.PI * 190 * t) * 0.4) * Math.exp(-t * 22)
  }
  return out
})()
const hat = (open: boolean) => {
  const n = Math.floor((open ? 0.12 : 0.05) * SR)
  const out = new Float32Array(n)
  let prev = 0
  for (let i = 0; i < n; i++) {
    const x = rand()
    out[i] = (x - prev) * Math.exp((-i / SR) * (open ? 40 : 90)) * (open ? 0.8 : 0.5)
    prev = x
  }
  return out
}
const HAT = hat(false)
const OPEN = hat(true)
const bass = (freq: number) => {
  const n = Math.floor(0.2 * SR)
  const out = new Float32Array(n)
  for (let i = 0; i < n; i++) out[i] = Math.sin((2 * Math.PI * freq * i) / SR) * Math.exp((-i / SR) * 6) * 0.6
  return out
}

/** Un compás = 16 pasos; cada paso lleva letras: k bombo, s caja, h hat cerrado, o hat abierto. `bassSteps` marca notas de bajo (multiplicador de 55 Hz o 0). */
function make(bpm: number, pattern: string[], seconds = 60, bassSteps?: number[]): Float32Array {
  const buf = new Float32Array(seconds * SR)
  const step = 60 / bpm / 4
  const put = (s: Float32Array, at: number, g: number) => {
    const i = Math.round(at * SR)
    for (let k = 0; k < s.length && i + k < buf.length; k++) buf[i + k] += s[k] * g
  }
  const bassCache = new Map<number, Float32Array>()
  for (let i = 0; i * step < seconds; i++) {
    const p = pattern[i % pattern.length]
    const t = i * step
    if (p.includes('k')) put(kick, t, 1)
    if (p.includes('s')) put(snare, t, 0.8)
    if (p.includes('h')) put(HAT, t, 0.5)
    if (p.includes('o')) put(OPEN, t, 0.8)
    const b = bassSteps?.[i % bassSteps.length] ?? 0
    if (b) {
      if (!bassCache.has(b)) bassCache.set(b, bass(55 * b))
      put(bassCache.get(b)!, t, 0.8)
    }
  }
  let max = 0
  for (const v of buf) max = Math.max(max, Math.abs(v))
  for (let i = 0; i < buf.length; i++) buf[i] /= max * 1.1
  return buf
}

const FOUR = ['k', '', '', '', 'k', '', '', '', 'k', '', '', '', 'k', '', '', '']
/** hi-hat abierto en cada contratiempo (pasos 2, 6, 10, 14) */
const OFFBEAT_HATS = ['k', '', 'o', '', 'k', '', 'o', '', 'k', '', 'o', '', 'k', '', 'o', '']
const HIPHOP = ['kh', '', 'h', '', 'sh', '', 'h', 'k', 'h', 'k', 'h', '', 'sh', '', 'h', '']
const DNB = ['kh', '', 'h', '', 'sh', '', 'h', '', 'h', 'k', 'h', '', 'sh', '', 'h', '']
const TECHNO = ['ko', '', 'h', '', 'ko', '', 'h', '', 'ko', '', 'h', '', 'ko', '', 'h', '']
/** bajo cada 5 semicorcheas: un periodo de 5/4 del pulso (el caso de «Thriller» a 145 que se leía como 116) */
const SYNC_BASS = [1, 0, 0, 0, 0, 1.2, 0, 0, 0, 0, 1.5, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1.2, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1.5, 0, 0, 0, 0]

const cases: { name: string; bpm: number; mono: () => Float32Array }[] = [
  { name: 'hip-hop 90', bpm: 90, mono: () => make(90, HIPHOP) },
  { name: 'house 128 con hi-hats a contratiempo', bpm: 128, mono: () => make(128, OFFBEAT_HATS) },
  { name: 'techno 140', bpm: 140, mono: () => make(140, TECHNO) },
  { name: 'bajo sincopado a 5/4 del pulso, 145', bpm: 145, mono: () => make(145, FOUR, 60, SYNC_BASS) },
  { name: 'drum & bass 174', bpm: 174, mono: () => make(174, DNB) },
  { name: 'drum & bass 176 (no debe quedarse en 88)', bpm: 176, mono: () => make(176, DNB) },
  { name: 'tempo lento 75', bpm: 75, mono: () => make(75, ['k', '', 'h', '', 's', '', 'h', '', '', 'k', 'h', '', 's', '', 'h', '']) },
]

describe('detector de BPM', () => {
  for (const c of cases) {
    it(`${c.name}: sale ${c.bpm} BPM con la rejilla sobre el bombo`, () => {
      const a = analyzeMono(c.mono(), SR)
      expect(Math.abs(a.bpm - c.bpm) / c.bpm).toBeLessThan(0.01)
      // El primer bombo está en t = 0: el primer pulso de la rejilla debe caer ahí (módulo un pulso), no a contratiempo.
      const period = 60 / a.bpm
      const off = a.beatOffset % period
      expect(Math.min(off, period - off)).toBeLessThan(0.03)
      expect(a.bpmConf).toBeGreaterThanOrEqual(0)
      expect(a.bpmConf).toBeLessThanOrEqual(1)
      expect(a.bpmAlt.every((x) => Math.abs(x - a.bpm) / a.bpm > 0.04)).toBe(true)
    })
  }

  it('un audio sin pulso (silencio) no revienta y devuelve 0', () => {
    const a = analyzeMono(new Float32Array(SR * 20), SR)
    expect(a.bpm).toBe(0)
    expect(a.beatPeriod).toBe(0)
  })

  it('un audio demasiado corto para estimar el tempo devuelve 0', () => {
    expect(analyzeMono(new Float32Array(SR * 2), SR).bpm).toBe(0)
  })
})

describe('espectro', () => {
  it('la FFT real (a mitad de tamaño) coincide con una DFT directa', () => {
    const n = 512
    const x = new Float32Array(n)
    const r = rng(7)
    for (let i = 0; i < n; i++) x[i] = r() + Math.sin((2 * Math.PI * 37 * i) / n)
    const mag = magnitudeSpectrum(x)
    for (const k of [1, 2, 6, 37, 100, 255]) {
      let re = 0
      let im = 0
      for (let i = 0; i < n; i++) {
        re += x[i] * Math.cos((2 * Math.PI * k * i) / n)
        im -= x[i] * Math.sin((2 * Math.PI * k * i) / n)
      }
      const want = Math.hypot(re, im)
      expect(Math.abs(mag[k] - want)).toBeLessThan(1e-3 * Math.max(1, want))
    }
  })
})
