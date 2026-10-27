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
  /** 0..1: seguridad del BPM frente al mejor candidato alternativo (bajo = revisar). */
  bpmConf: number
  /** Otros tempos plausibles (p. ej. el doble o la mitad). */
  bpmAlt: number[]
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

  const onset = onsetEnvelope(mono, frames)
  const est = estimateBpm(onset.all, fps, onset.kick)
  let beatOffset = 0
  let beatPeriod = 0
  let finalBpm = 0
  if (est) {
    finalBpm = est.bpm
    beatOffset = est.offset
    beatPeriod = 60 / est.bpm
  }
  return {
    wave, fps, frames, duration: n / sr, bpm: finalBpm, bpmConf: est?.conf ?? 0, bpmAlt: est?.alt ?? [],
    beatOffset, beatPeriod, key: detectKey(mono, sr),
  }
}

// ------------------------------------------------------------------ BPM
// Fuerza de ataque por cuadro (≈10 ms): flujo espectral logarítmico en 4 bandas. Se calcula sobre el audio
// a la mitad de frecuencia (11 025 Hz) con una FFT de 512 puntos: separa bien el bombo (30–150 Hz) del bajo
// (150–400 Hz), cosa que una envolvente de volumen no logra (un bajo sincopado se confundía con el pulso).
const FFT_N = 512
const FFT_HOP = HOP / 2
const BAND_BINS: [number, number, number][] = [
  // [primer bin, último bin, peso]; cada bin mide 11 025 / 512 ≈ 21.5 Hz
  [2, 6, 1.0], // 30–150 Hz: bombo
  [7, 18, 0.7], // 150–400 Hz: bajo
  [19, 116, 0.5], // 400–2 500 Hz: medios
  [117, 255, 0.4], // 2 500–5 500 Hz: agudos
]

function fftMags(re: Float32Array, im: Float32Array, cos: Float32Array, sin: Float32Array, rev: Uint16Array): void {
  const n = FFT_N
  for (let i = 0; i < n; i++) {
    const j = rev[i]
    if (j > i) {
      const t = re[i]
      re[i] = re[j]
      re[j] = t
    }
  }
  for (let size = 2; size <= n; size <<= 1) {
    const h = size >> 1
    const step = n / size
    for (let start = 0; start < n; start += size) {
      for (let k = 0, w = 0; k < h; k++, w += step) {
        const i = start + k
        const j = i + h
        const tr = re[j] * cos[w] + im[j] * sin[w]
        const ti = im[j] * cos[w] - re[j] * sin[w]
        re[j] = re[i] - tr
        im[j] = im[i] - ti
        re[i] += tr
        im[i] += ti
      }
    }
  }
}

export function onsetEnvelope(mono: Float32Array, frames: number): { all: Float32Array; kick: Float32Array } {
  const n = FFT_N
  const half = new Float32Array(Math.floor(mono.length / 2))
  for (let i = 0; i < half.length; i++) half[i] = 0.5 * (mono[2 * i] + mono[2 * i + 1])
  const win = new Float32Array(n)
  const cos = new Float32Array(n / 2)
  const sin = new Float32Array(n / 2)
  for (let i = 0; i < n; i++) win[i] = 0.5 - 0.5 * Math.cos((2 * Math.PI * i) / n)
  for (let i = 0; i < n / 2; i++) {
    cos[i] = Math.cos((2 * Math.PI * i) / n)
    sin[i] = Math.sin((2 * Math.PI * i) / n)
  }
  const rev = new Uint16Array(n)
  const bits = Math.log2(n)
  for (let i = 0; i < n; i++) {
    let r = 0
    for (let b = 0; b < bits; b++) if (i & (1 << b)) r |= 1 << (bits - 1 - b)
    rev[i] = r
  }
  const re = new Float32Array(n)
  const im = new Float32Array(n)
  const prev = new Float32Array(n / 2)
  const flux = BAND_BINS.map(() => new Float32Array(frames))
  for (let f = 0; f < frames; f++) {
    const end = (f + 1) * FFT_HOP // la ventana termina al final del cuadro
    const start = end - n
    for (let i = 0; i < n; i++) {
      const k = start + i
      re[i] = k >= 0 && k < half.length ? half[k] * win[i] : 0
      im[i] = 0
    }
    fftMags(re, im, cos, sin, rev)
    for (let bi = 0; bi < BAND_BINS.length; bi++) {
      const [b0, b1] = BAND_BINS[bi]
      let sum = 0
      for (let k = b0; k <= b1; k++) {
        const l = Math.log1p(Math.sqrt(re[k] * re[k] + im[k] * im[k]) * 20)
        const d = l - prev[k]
        if (d > 0 && f > 0) sum += d
        prev[k] = l
      }
      flux[bi][f] = sum / (b1 - b0 + 1)
    }
  }
  // Cada banda se escala por su percentil 95 para que ninguna aplaste a las demás.
  const all = new Float32Array(frames)
  const kick = new Float32Array(frames) // solo bombo y bajo: es lo que marca el pulso en la música de baile
  flux.forEach((arr, bi) => {
    const sorted = Float32Array.from(arr).sort()
    const p = sorted[Math.min(frames - 1, Math.floor(frames * 0.95))] || 1e-6
    const w = BAND_BINS[bi][2] / p
    for (let f = 0; f < frames; f++) {
      all[f] += arr[f] * w
      if (bi < 2) kick[f] += arr[f] * w
    }
  })
  return { all, kick }
}

const MIN_BPM = 70
const MAX_BPM = 180

export interface BpmEstimate {
  bpm: number
  offset: number
  /** 0..1: qué tan clara fue la victoria sobre el mejor candidato distinto (poco = dudoso). */
  conf: number
  /** Otros tempos plausibles, del más al menos probable. */
  alt: number[]
}

/**
 * BPM: la autocorrelación de los ataques de graves propone varios candidatos (picos) y cada uno se
 * PUNTÚA contra la canción completa con una rejilla de pulsos (refineGrid): gana el que mejor encaja.
 * Así un pico espurio (p. ej. un bajo sincopado a 5/4 del pulso) no se impone a la rejilla real.
 */
export function estimateBpm(env: Float32Array, fps: number, kick: Float32Array = env): BpmEstimate | null {
  // `env` y `kick` ya son curvas de ataques (onsetEnvelope): con `env` se proponen candidatos y con `kick` se puntúan.
  const m = Math.min(env.length, Math.floor(fps * 120)) // máximo 2 min (la parte central basta)
  const from = Math.max(0, Math.floor((env.length - m) / 2))
  const minLag = Math.floor((fps * 60) / 190)
  const maxLag = Math.ceil((fps * 60) / 60) * 4
  if (m < maxLag * 2) return null
  const autocorr = (src: Float32Array) => {
    const x = src.slice(from, from + m)
    let mu = 0
    for (const v of x) mu += v
    mu /= Math.max(1, m)
    for (let i = 0; i < m; i++) x[i] = Math.max(0, x[i] - mu)
    const r = new Float32Array(maxLag + 3)
    for (let lag = minLag; lag <= maxLag + 2; lag++) {
      let sum = 0
      for (let i = 0; i + lag < m; i++) sum += x[i] * x[i + lag]
      r[lag] = sum / (m - lag)
    }
    return r
  }
  const acAll = autocorr(env)
  const acKick = autocorr(kick)
  // Se combinan las dos curvas (cada una normalizada por su propio máximo): los agudos (hi-hats a contratiempo)
  // pueden proponer un pulso falso que el bombo desmiente, y al revés.
  let mAll = 1e-9
  let mKick = 1e-9
  for (let i = minLag; i < acAll.length; i++) {
    mAll = Math.max(mAll, acAll[i])
    mKick = Math.max(mKick, acKick[i])
  }
  const ac = new Float32Array(acAll.length)
  for (let i = minLag; i < ac.length; i++) ac[i] = 0.5 * (acAll[i] / mAll) + 0.5 * (acKick[i] / mKick)
  // Valor en un lag no entero: el pico real puede caer entre dos cuadros, así que se toma el máximo vecino.
  const near = (r: Float32Array, x: number) => {
    const i = Math.round(x)
    return Math.max(r[i - 1] ?? 0, r[i] ?? 0, r[i + 1] ?? 0)
  }
  const acAt = (x: number) => near(ac, x)
  const lagLo = Math.floor((fps * 60) / MAX_BPM)
  const lagHi = Math.ceil((fps * 60) / MIN_BPM)
  const sc = new Float32Array(lagHi + 2)
  for (let lag = lagLo - 1; lag <= lagHi + 1; lag++) sc[lag] = ac[lag] + 0.5 * acAt(lag * 2) + 0.25 * acAt(lag * 4)

  // Candidatos: picos locales, los mejores primero y separados al menos un 4 %.
  const peaks: { lag: number; score: number }[] = []
  for (let lag = lagLo; lag <= lagHi; lag++) {
    if (sc[lag] > 0 && sc[lag] >= sc[lag - 1] && sc[lag] >= sc[lag + 1]) {
      const a = sc[lag - 1], b = sc[lag], c = sc[lag + 1]
      const den = a - 2 * b + c
      const sh = den !== 0 ? Math.max(-1, Math.min(1, (0.5 * (a - c)) / den)) : 0
      peaks.push({ lag: lag + sh, score: b })
    }
  }
  peaks.sort((x, y) => y.score - x.score)
  const cands: { lag: number; score: number }[] = []
  for (const p of peaks) {
    if (cands.every((c) => Math.abs(c.lag - p.lag) / c.lag > 0.04)) cands.push(p)
    if (cands.length >= 6) break
  }
  if (!cands.length) return null
  const top = cands[0].score || 1e-9
  const prior = (b: number) => Math.exp(-0.5 * Math.pow(Math.log2(b / 120) / 0.8, 2)) // leve preferencia por 120
  // Cada candidato se puntúa con la periodicidad (autocorrelación) y, de desempate, con qué tan bien encaja
  // una rejilla de pulsos sobre bombo y bajo en TODA la canción.
  const grids = cands.map((c) => refineGrid(kick, fps, (60 * fps) / c.lag, 0.04))
  const gmax = Math.max(1e-9, ...grids.map((g) => g.score))
  const results = cands.map((c, i) => {
    const g = grids[i]
    const total = (c.score / top) * (0.9 + 0.1 * prior(g.bpm)) * (0.85 + 0.15 * (g.score / gmax))
    return { bpm: g.bpm, lag: c.lag, total }
  })
  results.sort((x, y) => y.total - x.total)
  const best = results[0]
  if (!(best.total > 0)) return null
  // Confianza: ventaja sobre el mejor tempo realmente distinto (ni un vecino del mismo pico ni su octava).
  const distinct = (r: { bpm: number }) => {
    const q = r.bpm / best.bpm
    return Math.abs(q - 1) > 0.04 && Math.abs(q - 2) > 0.08 && Math.abs(q - 0.5) > 0.04
  }
  const rival = results.find(distinct)
  const conf = rival ? Math.max(0, Math.min(1, 1 - rival.total / best.total)) : 1
  // Ambigüedad de octava (72 vs 145): si la mitad del periodo también tiene un ataque claro, el tempo es el doble.
  let lag = best.lag
  while (lag > 1) {
    const half = lag / 2
    if (half < lagLo || (60 * fps) / half > 175) break // más de 175 BPM ya casi nunca es el pulso
    if (near(acKick, half) < 0.7 * near(acKick, lag)) break
    lag = half
  }
  const bpmFinal = (60 * fps) / lag
  const alt: number[] = []
  for (const r of results) {
    const v = Math.round(r.bpm * 10) / 10
    if (Math.abs(r.bpm / bpmFinal - 1) > 0.04 && !alt.some((x) => Math.abs(x / v - 1) < 0.04)) alt.push(v)
    if (alt.length >= 3) break
  }
  const g = refineGrid(kick, fps, bpmFinal, 1)
  return { bpm: Math.round(g.bpm * 10) / 10, offset: g.offset, conf, alt }
}

/**
 * Afina el BPM (±span) y encuentra la fase de la rejilla: la que hace coincidir más pulsos con ataques de
 * graves en TODO el tema. Así la rejilla no se desplaza aunque el BPM estimado tuviera décimas de error.
 * `score` mide cuánta fuerza de ataque hay en los pulsos menos la mitad de la que hay a medio pulso
 * (penaliza rejillas a mitad de la velocidad real, donde los golpes quedan entre pulsos).
 */
export function refineGrid(onset: Float32Array, fps: number, bpm0: number, span = 1 / 120): { bpm: number; offset: number; score: number } {
  const m = onset.length
  const on = onset
  // suavizado de 3 puntos: tolera medio cuadro de error
  const s = new Float32Array(m)
  for (let i = 1; i < m - 1; i++) s[i] = 0.25 * on[i - 1] + 0.5 * on[i] + 0.25 * on[i + 1]
  // Se resta el nivel de fondo (media): solo cuentan los ataques que sobresalen.
  let avg = 0
  for (let i = 0; i < m; i++) avg += s[i]
  avg /= Math.max(1, m)
  for (let i = 0; i < m; i++) s[i] = Math.max(0, s[i] - avg)
  const at = (t: number) => {
    const i = Math.floor(t)
    if (i < 0 || i >= m - 1) return 0
    const f = t - i
    return s[i] * (1 - f) + s[i + 1] * f
  }
  // Un ataque «fuerte» es el percentil 95 del tema. Se mide cuánta fuerza hay en los puntos de la rejilla
  // (desplazada `shift` de pulso): el pulso real es donde caen los golpes MÁS fuertes (bombo), no los débiles.
  const sorted = Float32Array.from(s).sort()
  const thr = Math.max(1e-6, sorted[Math.min(m - 1, Math.floor(m * 0.95))])
  const hits = (b: number, ph: number, shift: number) => {
    const P = (60 * fps) / b
    let sum = 0
    let cnt = 0
    for (let t = ph + shift * P; t < m - 1; t += P) {
      sum += Math.min(1.5, at(t) / thr)
      cnt++
    }
    return cnt ? sum / cnt : 0
  }
  const lo = span >= 1 ? bpm0 - span : bpm0 * (1 - span)
  const hi = span >= 1 ? bpm0 + span : bpm0 * (1 + span)
  const step = Math.max(0.02, (hi - lo) / 100)
  let bestScore = -1
  let bestBpm = bpm0
  let bestPhase = 0
  for (let b = lo; b <= hi + 1e-6; b += step) {
    const P = (60 * fps) / b
    for (let ph = 0; ph < P; ph += 0.5) {
      let sum = 0
      let cnt = 0
      for (let t = ph; t < m - 1; t += P) {
        sum += at(t)
        cnt++
      }
      const v = cnt ? sum / cnt : 0
      if (v > bestScore) {
        bestScore = v
        bestBpm = b
        bestPhase = ph
      }
    }
  }
  return { bpm: bestBpm, offset: bestPhase / fps, score: Math.max(0, hits(bestBpm, bestPhase, 0) - 0.5 * hits(bestBpm, bestPhase, 0.5)) }
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
