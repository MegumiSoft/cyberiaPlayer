// Núcleo del análisis (sin DOM): se ejecuta en un Worker. Recibe audio mono y devuelve la forma de onda
// en 3 bandas, el BPM, la rejilla de tiempos y la tonalidad.

export interface KeyResult {
  /** 0 = C … 11 = B */
  pc: number
  minor: boolean
  /** 0..1: cuánta seguridad hay en la tonalidad */
  conf: number
}

export interface Analysis {
  /** Por cada cuadro (fps por segundo): graves, medios, agudos (0..255), intercalados. */
  wave: Uint8Array
  fps: number
  frames: number
  duration: number
  bpm: number // 0 si no se pudo estimar
  /** Segundos hasta el primer pulso de la rejilla y duración de un pulso. 0 si no hay BPM. */
  beatOffset: number
  beatPeriod: number
  key: KeyResult | null
}

export const ANALYSIS_SR = 22050
const HOP = 220 // ≈ 10 ms

export function analyzeMono(mono: Float32Array, sr: number = ANALYSIS_SR): Analysis {
  const n = mono.length
  const frames = Math.floor(n / HOP)
  const fps = sr / HOP
  const wave = new Uint8Array(frames * 3)
  const low = new Float32Array(frames)
  const mid = new Float32Array(frames)
  const high = new Float32Array(frames)

  // Separación en tres bandas con filtros de un polo en cascada: <220 Hz, 220–2500 Hz, >2500 Hz.
  const a1 = 1 - Math.exp((-2 * Math.PI * 220) / sr)
  const a2 = 1 - Math.exp((-2 * Math.PI * 2500) / sr)
  let l1 = 0, l2 = 0, m1 = 0, m2 = 0
  for (let f = 0; f < frames; f++) {
    let sl = 0, sm = 0, sh = 0
    const base = f * HOP
    for (let i = 0; i < HOP; i++) {
      const x = mono[base + i]
      l1 += a1 * (x - l1)
      l2 += a1 * (l1 - l2) // dos polos: caída más pronunciada para los graves
      m1 += a2 * (x - m1)
      m2 += a2 * (m1 - m2)
      const lo = l2
      const md = m2 - l2
      const hi = x - m2
      sl += lo * lo
      sm += md * md
      sh += hi * hi
    }
    low[f] = Math.sqrt(sl / HOP)
    mid[f] = Math.sqrt(sm / HOP)
    high[f] = Math.sqrt(sh / HOP)
  }

  // Cada banda se normaliza por su percentil 97 para que graves, medios y agudos se vean con proporciones parecidas.
  const norm = (arr: Float32Array, lane: number, gain: number) => {
    const sorted = Float32Array.from(arr).sort()
    const p = sorted[Math.min(sorted.length - 1, Math.floor(sorted.length * 0.97))] || 1e-6
    for (let f = 0; f < frames; f++) {
      const v = Math.pow(Math.min(1.25, arr[f] / p), 0.8) * gain
      wave[f * 3 + lane] = Math.round(Math.max(0, Math.min(1, v)) * 255)
    }
  }
  norm(low, 0, 1)
  norm(mid, 1, 0.78)
  norm(high, 2, 0.6)

  const bpm = estimateBpm(low, fps)
  let beatOffset = 0
  let beatPeriod = 0
  let finalBpm = bpm
  if (bpm > 0) {
    const g = refineGrid(low, fps, bpm)
    finalBpm = Math.round(g.bpm * 10) / 10
    beatOffset = g.offset
    beatPeriod = 60 / g.bpm
  }
  return { wave, fps, frames, duration: n / sr, bpm: finalBpm, beatOffset, beatPeriod, key: detectKey(mono, sr) }
}

// ------------------------------------------------------------------ BPM
function attack(env: Float32Array, from: number, m: number): Float32Array {
  const on = new Float32Array(m)
  for (let i = 1; i < m; i++) {
    const d = Math.log1p(env[from + i] * 40) - Math.log1p(env[from + i - 1] * 40)
    on[i] = d > 0 ? d : 0
  }
  return on
}

/** BPM por autocorrelación de la fuerza de ataque de los graves, con peso a múltiplos del compás. */
export function estimateBpm(env: Float32Array, fps: number): number {
  const m = Math.min(env.length, Math.floor(fps * 120)) // máximo 2 min (la parte central basta)
  const from = Math.max(0, Math.floor((env.length - m) / 2))
  const on = attack(env, from, m)
  let mean = 0
  for (const v of on) mean += v
  mean /= Math.max(1, m)
  for (let i = 0; i < m; i++) on[i] = Math.max(0, on[i] - mean)

  const minLag = Math.floor((fps * 60) / 190)
  const maxLag = Math.ceil((fps * 60) / 60) * 4
  if (m < maxLag * 2) return 0
  const ac = new Float32Array(maxLag + 2)
  for (let lag = minLag; lag <= maxLag + 1; lag++) {
    let s = 0
    for (let i = 0; i + lag < m; i++) s += on[i] * on[i + lag]
    ac[lag] = s / (m - lag)
  }
  let best = 0
  let bestScore = 0
  const lagMax2 = Math.ceil((fps * 60) / 70)
  for (let lag = Math.floor((fps * 60) / 180); lag <= lagMax2; lag++) {
    const score0 = ac[lag] + 0.5 * (ac[lag * 2] ?? 0) + 0.25 * (ac[lag * 4] ?? 0)
    const bpm = (60 * fps) / lag
    const prior = Math.exp(-0.5 * Math.pow(Math.log2(bpm / 120) / 0.8, 2)) // leve preferencia por 120
    const score = score0 * (0.6 + 0.4 * prior)
    if (score > bestScore) {
      bestScore = score
      best = lag
    }
  }
  if (!best || bestScore <= 0) return 0
  // Ambigüedad de octava (75 vs 150): si el medio periodo también tiene un ataque claro, el tempo es el doble.
  while (best > 1) {
    const half = Math.round(best / 2)
    if (half < Math.floor((fps * 60) / 180)) break
    const near = Math.max(ac[half - 1], ac[half], ac[half + 1])
    if (near < 0.5 * ac[best]) break
    best = half
  }
  const a = ac[best - 1]
  const b = ac[best]
  const c = ac[best + 1]
  const den = a - 2 * b + c
  const shift = den !== 0 ? (0.5 * (a - c)) / den : 0
  let bpm = (60 * fps) / (best + Math.max(-1, Math.min(1, shift)))
  while (bpm < 70) bpm *= 2
  while (bpm > 180) bpm /= 2
  return Math.round(bpm * 10) / 10
}

/**
 * Afina el BPM (±1) y encuentra la fase de la rejilla: la que hace coincidir más pulsos con ataques de
 * graves en TODO el tema. Así la rejilla no se desplaza aunque el BPM estimado tuviera décimas de error.
 */
export function refineGrid(low: Float32Array, fps: number, bpm0: number): { bpm: number; offset: number } {
  const m = low.length
  const on = attack(low, 0, m)
  // suavizado de 3 puntos: tolera medio cuadro de error
  const s = new Float32Array(m)
  for (let i = 1; i < m - 1; i++) s[i] = 0.25 * on[i - 1] + 0.5 * on[i] + 0.25 * on[i + 1]
  const at = (t: number) => {
    const i = Math.floor(t)
    if (i < 0 || i >= m - 1) return 0
    const f = t - i
    return s[i] * (1 - f) + s[i + 1] * f
  }
  let bestScore = -1
  let bestBpm = bpm0
  let bestPhase = 0
  for (let b = bpm0 - 1; b <= bpm0 + 1.0001; b += 0.02) {
    const P = (60 * fps) / b
    for (let ph = 0; ph < P; ph += 0.5) {
      let sum = 0
      let cnt = 0
      for (let t = ph; t < m - 1; t += P) {
        sum += at(t)
        cnt++
      }
      const sc = cnt ? sum / cnt : 0
      if (sc > bestScore) {
        bestScore = sc
        bestBpm = b
        bestPhase = ph
      }
    }
  }
  return { bpm: bestBpm, offset: bestPhase / fps }
}

// ------------------------------------------------------------------ tonalidad
// Perfiles de Faraldo, ajustados a música electrónica de baile (la forma importa, no la escala).
const EDMA_MAJOR = [1.0, 0.29, 0.5, 0.4, 0.6, 0.56, 0.32, 0.8, 0.31, 0.45, 0.42, 0.39]
const EDMA_MINOR = [1.0, 0.31, 0.44, 0.58, 0.33, 0.49, 0.29, 0.78, 0.43, 0.29, 0.53, 0.32]
/** En la música de baile casi todo es menor: un empate dudoso se lo lleva el modo menor. */
const MINOR_BIAS = 0.05
/** Peso del voto del bajo: la nota grave más fuerte suele ser la tónica. */
const BASS_WEIGHT = 0.12
/** Cada pico también vota por f/2, f/3 y f/4 (sus armónicos apuntan a la fundamental, no a la quinta). */
const HARMONIC_DECAY = 0.6
const HARMONICS = 4

function corr(a: number[], b: number[]): number {
  let ma = 0, mb = 0
  for (let i = 0; i < 12; i++) { ma += a[i]; mb += b[i] }
  ma /= 12; mb /= 12
  let sab = 0, saa = 0, sbb = 0
  for (let i = 0; i < 12; i++) {
    const x = a[i] - ma, y = b[i] - mb
    sab += x * y; saa += x * x; sbb += y * y
  }
  return saa && sbb ? sab / Math.sqrt(saa * sbb) : 0
}

/** Transformada rápida de Fourier (radix-2, in situ). */
function fft(re: Float32Array, im: Float32Array): void {
  const n = re.length
  for (let i = 1, j = 0; i < n; i++) {
    let bit = n >> 1
    for (; j & bit; bit >>= 1) j ^= bit
    j ^= bit
    if (i < j) {
      const tr = re[i]; re[i] = re[j]; re[j] = tr
      const ti = im[i]; im[i] = im[j]; im[j] = ti
    }
  }
  for (let len = 2; len <= n; len <<= 1) {
    const ang = (-2 * Math.PI) / len
    const wr = Math.cos(ang), wi = Math.sin(ang)
    for (let i = 0; i < n; i += len) {
      let cr = 1, ci = 0
      for (let k = 0; k < len / 2; k++) {
        const ur = re[i + k], ui = im[i + k]
        const xr = re[i + k + len / 2], xi = im[i + k + len / 2]
        const vr = xr * cr - xi * ci, vi = xr * ci + xi * cr
        re[i + k] = ur + vr; im[i + k] = ui + vi
        re[i + k + len / 2] = ur - vr; im[i + k + len / 2] = ui - vi
        const t = cr * wr - ci * wi
        ci = cr * wi + ci * wr
        cr = t
      }
    }
  }
}

/** Cromagrama (12 clases de nota) sobre la parte central del tema, correlacionado con los perfiles de Krumhansl. */
export function detectKey(mono: Float32Array, sr: number): KeyResult | null {
  const N = 8192
  const hop = 4096
  const maxSec = 120
  const total = mono.length
  const len = Math.min(total, Math.floor(maxSec * sr))
  const start = Math.floor((total - len) / 2)
  if (len < N * 4) return null

  const win = new Float32Array(N)
  for (let i = 0; i < N; i++) win[i] = 0.5 - 0.5 * Math.cos((2 * Math.PI * i) / (N - 1))
  const re = new Float32Array(N)
  const im = new Float32Array(N)
  const binHz = sr / N
  const lo = Math.ceil(80 / binHz)
  const hi = Math.floor(2200 / binHz)
  const mag = new Float32Array(hi + 2)

  // 1.ª pasada: picos espectrales, para estimar la afinación (desviación media respecto a A=440) y acumular
  const peaks: { pc: number; dev: number; w: number }[] = []
  const frameList: { b: number[]; w: number[] }[] = []
  for (let off = start; off + N <= start + len; off += hop) {
    for (let i = 0; i < N; i++) { re[i] = mono[off + i] * win[i]; im[i] = 0 }
    fft(re, im)
    let fmax = 0
    for (let k = lo - 1; k <= hi + 1; k++) {
      mag[k - (lo - 1)] = Math.sqrt(re[k] * re[k] + im[k] * im[k])
      if (mag[k - (lo - 1)] > fmax) fmax = mag[k - (lo - 1)]
    }
    if (fmax <= 0) continue
    const bins: number[] = []
    const ws: number[] = []
    for (let k = lo; k <= hi; k++) {
      const j = k - (lo - 1)
      const v = mag[j]
      if (v > mag[j - 1] && v >= mag[j + 1] && v > fmax * 0.05) {
        // interpolación parabólica de la frecuencia del pico
        const a = mag[j - 1], c = mag[j + 1]
        const den = a - 2 * v + c
        const d = den !== 0 ? (0.5 * (a - c)) / den : 0
        const f = (k + d) * binHz
        const midi = 12 * Math.log2(f / 440) + 69
        const r = Math.round(midi)
        bins.push(f)
        ws.push(Math.sqrt(v / fmax))
        peaks.push({ pc: ((r % 12) + 12) % 12, dev: midi - r, w: Math.sqrt(v / fmax) })
      }
    }
    frameList.push({ b: bins, w: ws })
  }
  if (!frameList.length) return null

  // desviación de afinación: media circular ponderada (en semitonos, entre -0.5 y 0.5)
  let sx = 0, sy = 0
  for (const p of peaks) { sx += p.w * Math.cos(2 * Math.PI * p.dev); sy += p.w * Math.sin(2 * Math.PI * p.dev) }
  const tune = Math.atan2(sy, sx) / (2 * Math.PI)

  const chroma = new Array<number>(12).fill(0)
  const bass = new Array<number>(12).fill(0)
  for (const fr of frameList) {
    for (let i = 0; i < fr.b.length; i++) {
      const f = fr.b[i]
      const w = fr.w[i]
      let wt = 1
      for (let h = 1; h <= HARMONICS; h++, wt *= HARMONIC_DECAY) {
        const fh = f / h
        if (fh < 40) break
        const midi = 12 * Math.log2(fh / 440) + 69 - tune
        const pc = ((Math.round(midi) % 12) + 12) % 12
        chroma[pc] += w * wt
      }
      if (f >= 45 && f <= 190) {
        const pc = ((Math.round(12 * Math.log2(f / 440) + 69 - tune) % 12) + 12) % 12
        bass[pc] += w
      }
    }
  }
  const bmax = Math.max(...bass, 1e-9)

  const scores: { r: number; pc: number; minor: boolean }[] = []
  for (let pc = 0; pc < 12; pc++) {
    const rot = (prof: number[]) => prof.map((_, i) => prof[(i - pc + 12) % 12])
    const bv = BASS_WEIGHT * (bass[pc] / bmax)
    scores.push({ r: corr(chroma, rot(EDMA_MAJOR)) + bv, pc, minor: false })
    scores.push({ r: corr(chroma, rot(EDMA_MINOR)) + bv + MINOR_BIAS, pc, minor: true })
  }
  scores.sort((a, b) => b.r - a.r)
  const [b1, b2] = scores
  const conf = Math.max(0, Math.min(1, 0.5 * Math.min(1, (b1.r - b2.r) / 0.08) + 0.5 * Math.max(0, Math.min(1, (b1.r - 0.4) / 0.4))))
  return { pc: b1.pc, minor: b1.minor, conf }
}

// ------------------------------------------------------------------ nombres de la tonalidad
const MAJOR_NAMES = ['C', 'Db', 'D', 'Eb', 'E', 'F', 'F#', 'G', 'Ab', 'A', 'Bb', 'B']
const MINOR_NAMES = ['C', 'C#', 'D', 'Eb', 'E', 'F', 'F#', 'G', 'G#', 'A', 'Bb', 'B']

export function camelot(k: KeyResult): string {
  const major = k.minor ? (k.pc + 3) % 12 : k.pc // la menor comparte número con su relativa mayor
  return `${((major * 7 + 7) % 12) + 1}${k.minor ? 'A' : 'B'}`
}

export function keyName(k: KeyResult): string {
  return `${(k.minor ? MINOR_NAMES : MAJOR_NAMES)[k.pc]} ${k.minor ? 'min' : 'maj'}`
}

export function keyText(k: KeyResult | null, notation: 'camelot' | 'note'): string {
  if (!k) return '--'
  return notation === 'camelot' ? camelot(k) : keyName(k)
}
