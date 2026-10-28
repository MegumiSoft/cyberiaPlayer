import { markRaw, reactive } from 'vue'
import { api, inWails } from '../api'
import type { LibPlaylist, LibTrack } from '../types'
import { analyze, analyzeViaBackend, ANALYSIS_VERSION, needsBackendDecode, type Analysis, type KeyResult } from './analysis'
import { b64ToBytes } from './bytes'
import { persistAnalysis } from './libanalysis'
import { gridOf, metaOf } from './trackmeta'
import { emptyFrame, type Frame } from './visualizer'

// Motor del reproductor: <audio> -> 10 filtros peaking (EQ) -> analizador -> volumen -> salida.
// El espectro del analizador alimenta los visualizadores; los graves disparan la detección de "beat".

export const EQ_FREQS = [60, 170, 310, 600, 1000, 3000, 6000, 12000, 14000, 16000]
export const EQ_LABELS = ['60', '170', '310', '600', '1K', '3K', '6K', '12K', '14K', '16K']
export const EQ_RANGE = 12 // dB hacia cada lado

export interface QueueItem {
  track: LibTrack
  playlist: string
  url: string
}

export const player = reactive({
  queue: [] as QueueItem[],
  index: -1,
  playing: false,
  loading: false,
  time: 0,
  duration: 0,
  volume: 0.8,
  shuffle: false,
  loop: 'off' as 'off' | 'all' | 'one',
  eq: new Array(EQ_FREQS.length).fill(0) as number[],
  eqOn: true,
  error: '',
  /** Progreso (0-100) mientras la canción se prepara para sonar. */
  loadPct: 0,
  /** Análisis de la canción actual (null mientras se calcula o si es demasiado grande). */
  analysis: null as Analysis | null,
  bpm: 0,
  key: null as KeyResult | null,
  analyzing: false,
  /** Bitácora de tiempos de la última canción (para diagnosticar arranques lentos). */
  log: [] as string[],
})

let t0 = 0
/** Anota un hito con el tiempo transcurrido desde el clic en una canción. */
function mark(msg: string): void {
  const ms = t0 ? Math.round(performance.now() - t0) : 0
  const line = `+${ms} ms  ${msg}`
  player.log.push(line)
  if (player.log.length > 40) player.log.shift()
  console.info('[cyberia]', line)
}

let audio: HTMLAudioElement | null = null
let ctx: AudioContext | null = null
let analyser: AnalyserNode | null = null
let master: GainNode | null = null
let filters: BiquadFilterNode[] = []
let freqBuf = new Uint8Array(0)
let waveBuf = new Uint8Array(0)
let order: number[] = []
let playToken = 0
let blobUrl = ''
let stallTimer: ReturnType<typeof setTimeout> | undefined
const forceFallback = new URLSearchParams(location.search).has('nodirect')

/** Si el webview no deja cargar el audio por HTTP, la app pasa a leer los archivos por el backend. */
// En macOS el WKWebView de Wails no reproduce audio de http://127.0.0.1; intentarlo primero provocaba el
// arranque que "se corta y vuelve a empezar". Allí se usa directamente la lectura por el backend.
const isMacWails = inWails() && /Mac/i.test(navigator.platform || navigator.userAgent)
export const flags = { directFailed: isMacWails }
/** Mientras la comprobación inicial no termina, el primer play espera su resultado en vez de adivinar. */
let directPending: Promise<void> | null = null

const MIME: Record<string, string> = {
  mp3: 'audio/mpeg', m4a: 'audio/mp4', mp4: 'audio/mp4', aac: 'audio/aac', ogg: 'audio/ogg', oga: 'audio/ogg',
  opus: 'audio/ogg', flac: 'audio/flac', wav: 'audio/wav', webm: 'audio/webm',
}

// --------------------------------------------------------------- obtención de los bytes (respaldo por el backend)
const CHUNK = 1 << 20 // 1 MB por llamada: la interfaz sigue viva y se puede mostrar el progreso
const bytesCache = new Map<string, ArrayBuffer>() // LRU pequeña: la actual, la siguiente y alguna más
const inflight = new Map<string, Promise<ArrayBuffer>>()
const analysisCache = new Map<string, Analysis>()
const CACHE_MAX = 4

function remember(path: string, buf: ArrayBuffer) {
  bytesCache.delete(path)
  bytesCache.set(path, buf)
  while (bytesCache.size > CACHE_MAX) bytesCache.delete(bytesCache.keys().next().value as string)
}

/** Lee un archivo completo por el backend, en trozos y 3 a la vez, avisando del progreso. */
function fetchViaBackend(track: LibTrack, onProgress?: (pct: number) => void): Promise<ArrayBuffer> {
  const hit = bytesCache.get(track.path)
  if (hit) {
    remember(track.path, hit)
    onProgress?.(100)
    return Promise.resolve(hit)
  }
  const running = inflight.get(track.path)
  if (running) {
    return running.then((b) => (onProgress?.(100), b))
  }
  const size = track.size
  const out = new Uint8Array(size)
  const total = Math.ceil(size / CHUNK)
  let nextChunk = 0
  let done = 0
  const worker = async () => {
    while (nextChunk < total) {
      const k = nextChunk++
      const off = k * CHUNK
      const b64 = await api.readTrackChunk(track.path, off, Math.min(CHUNK, size - off))
      const bytes = b64ToBytes(b64)
      out.set(bytes, off)
      done++
      onProgress?.(Math.round((done / total) * 100))
    }
  }
  const p = Promise.all([worker(), worker(), worker()])
    .then(() => {
      remember(track.path, out.buffer)
      return out.buffer
    })
    .finally(() => inflight.delete(track.path))
  inflight.set(track.path, p)
  return p
}

const toBlobUrl = (buf: ArrayBuffer, ext: string): string => {
  if (blobUrl) URL.revokeObjectURL(blobUrl)
  blobUrl = URL.createObjectURL(new Blob([buf], { type: MIME[ext.toLowerCase()] ?? 'audio/mpeg' }))
  return blobUrl
}

async function loadViaBackend(token: number): Promise<void> {
  const it = player.queue[player.index]
  if (!it || !audio) return
  flags.directFailed = true
  player.loadPct = 0
  if (!audio.paused) audio.pause() // se calla la anterior: el cargador indica que la nueva viene en camino
  mark('leyendo archivo por el backend')
  try {
    const buf = await fetchViaBackend(it.track, (pct) => {
      if (token === playToken) player.loadPct = pct
    })
    if (token !== playToken || !audio) return // el usuario ya cambió de canción
    mark('bytes listos')
    audio.src = toBlobUrl(buf, it.track.ext)
    await audio.play()
  } catch (e) {
    if (token !== playToken) return
    if ((e as Error)?.name === 'AbortError') return
    player.loading = false
    player.error = 'No se pudo reproducir este archivo'
  }
}

/** Precarga (solo con el respaldo activo) para que al pulsar play la canción ya esté lista. */
/** Olvida lo que había en memoria de una canción cuyo archivo se reescribió. */
export function forgetTrack(path: string): void {
  bytesCache.delete(path)
  inflight.delete(path)
  analysisCache.delete(path)
}

export function prefetch(track: LibTrack | undefined): void {
  if (!track || !flags.directFailed || bytesCache.has(track.path) || inflight.has(track.path)) return
  void fetchViaBackend(track).catch(() => undefined)
}

// --------------------------------------------------------------- análisis (forma de onda + BPM)
async function bytesForAnalysis(it: QueueItem): Promise<ArrayBuffer> {
  const hit = bytesCache.get(it.track.path)
  if (hit) return hit
  if (flags.directFailed) return fetchViaBackend(it.track)
  const res = await fetch(it.url)
  return res.arrayBuffer()
}

/** Espera a que la canción lleve sonando unos segundos sin cortes antes de gastar CPU en analizarla. */
function whenStable(token: number, fn: () => void, tries = 0): void {
  setTimeout(() => {
    if (token !== playToken) return
    const stable = !!audio && !audio.paused && !player.loading && audio.currentTime > 2
    if (stable || tries >= 8) fn()
    else whenStable(token, fn, tries + 1)
  }, tries === 0 ? 2500 : 1000)
}

/** Reconstruye un análisis guardado en disco (así la onda aparece al instante y no se vuelve a calcular). */
async function analysisFromDisk(it: QueueItem): Promise<Analysis | null> {
  const st = metaOf(it.track.path).analysis
  if (!st || st.version !== ANALYSIS_VERSION || st.size !== it.track.size) return null
  try {
    const b64 = await api.loadWave(it.track.path, it.track.size)
    if (!b64) return null
    const wave = b64ToBytes(b64)
    if (wave.length < 3) return null
    return {
      wave,
      fps: 22050 / 220,
      frames: Math.floor(wave.length / 3),
      duration: st.duration,
      bpm: st.bpm,
      bpmConf: st.bpmConf ?? 1,
      bpmAlt: st.bpmAlt ?? [],
      beatOffset: st.beatOffset,
      beatPeriod: st.bpm > 0 ? 60 / st.bpm : 0,
      key: st.hasKey ? { pc: st.keyPc, minor: st.keyMinor, conf: st.keyConf } : null,
    }
  } catch {
    return null
  }
}

async function startAnalysis(it: QueueItem, token: number): Promise<void> {
  const cached = analysisCache.get(it.track.path)
  if (cached) return applyAnalysis(cached, token)
  player.analyzing = true
  try {
    let a: Analysis
    if (needsBackendDecode(it.track)) {
      mark('análisis: decodificando en el backend')
      a = await analyzeViaBackend(it.track.path, () => token !== playToken)
    } else {
      const buf = await bytesForAnalysis(it)
      if (token !== playToken) return
      mark('análisis: decodificando')
      a = await analyze(buf)
    }
    if (token !== playToken) return
    mark('análisis listo')
    analysisCache.set(it.track.path, a)
    while (analysisCache.size > 30) analysisCache.delete(analysisCache.keys().next().value as string)
    persistAnalysis(it.track, a)
    applyAnalysis(a, token)
  } catch {
    // sin forma de onda (o análisis descartado al saltar a otra canción): la interfaz usa el deslizador normal
  } finally {
    if (token === playToken) player.analyzing = false
  }
}

function applyAnalysis(a: Analysis, token: number) {
  if (token !== playToken) return
  player.analysis = markRaw(a)
  player.key = a.key
  syncGrid()
}

/** Pone en el reproductor el BPM de la rejilla vigente (editada o detectada) y la mascota baila a ese tempo. */
export function syncGrid(): void {
  const g = gridOf(current()?.track.path)
  player.bpm = g?.bpm ?? 0
  if (g) {
    beatState.bpm = g.bpm
    beatState.locked = true
  }
}

/** Pulso detectado: lo lee la mascota para bailar al ritmo (no es reactivo a propósito). */
export const beatState = { bpm: 120, last: 0, count: 0, intervals: [] as number[], locked: false }

const frame: Frame = emptyFrame()
let bassAvg = 0
let lastBeat = 0

/** Posición exacta de reproducción (el estado reactivo solo se actualiza unas veces por segundo). */
export const playbackTime = (): number => audio?.currentTime ?? 0

export const current = (): QueueItem | null => player.queue[player.index] ?? null

function ensureAudio(): HTMLAudioElement {
  if (audio) return audio
  const a = new Audio()
  a.crossOrigin = 'anonymous' // imprescindible para que el analizador no devuelva ceros
  a.preload = 'auto'
  a.volume = 1
  a.addEventListener('timeupdate', () => (player.time = a.currentTime))
  a.addEventListener('durationchange', () => (player.duration = Number.isFinite(a.duration) ? a.duration : 0))
  a.addEventListener('play', () => (player.playing = true))
  a.addEventListener('pause', () => (player.playing = false))
  a.addEventListener('waiting', () => ((player.loading = true), mark('waiting')))
  a.addEventListener('playing', () => ((player.loading = false), (player.error = ''), mark('playing')))
  a.addEventListener('canplay', () => ((player.loading = false), mark('canplay')))
  a.addEventListener('ended', () => {
    if (a.src === SILENT) return // el silencio de desbloqueo no es una canción
    next(true)
  })
  a.addEventListener('error', () => {
    if (a.src === SILENT) return
    mark('error del <audio>')
    if (a.src && a.src === blobUrl) {
      player.loading = false
      player.playing = false
      player.error = 'No se pudo reproducir este archivo'
    } else void loadViaBackend(playToken)
  })
  audio = a
  return a
}

function ensureGraph(): void {
  if (ctx) return
  const a = ensureAudio()
  const AC = window.AudioContext ?? (window as unknown as { webkitAudioContext: typeof AudioContext }).webkitAudioContext
  ctx = new AC()
  const src = ctx.createMediaElementSource(a)
  filters = EQ_FREQS.map((f, i) => {
    const b = ctx!.createBiquadFilter()
    b.type = 'peaking'
    b.frequency.value = f
    b.Q.value = 1.1
    b.gain.value = player.eqOn ? player.eq[i] : 0
    return b
  })
  analyser = ctx.createAnalyser()
  analyser.fftSize = 2048
  analyser.smoothingTimeConstant = 0.78
  master = ctx.createGain()
  master.gain.value = player.volume
  let node: AudioNode = src
  for (const f of filters) {
    node.connect(f)
    node = f
  }
  node.connect(analyser)
  analyser.connect(master)
  master.connect(ctx.destination)
  freqBuf = new Uint8Array(analyser.frequencyBinCount)
  waveBuf = new Uint8Array(analyser.fftSize)
}

function rebuildOrder(keepCurrent: boolean): void {
  const n = player.queue.length
  order = Array.from({ length: n }, (_, i) => i)
  if (player.shuffle) {
    for (let i = n - 1; i > 0; i--) {
      const j = Math.floor(Math.random() * (i + 1))
      ;[order[i], order[j]] = [order[j], order[i]]
    }
    if (keepCurrent && player.index >= 0) {
      const p = order.indexOf(player.index)
      ;[order[0], order[p]] = [order[p], order[0]]
    }
  }
}

export function setQueue(items: QueueItem[], start: number): void {
  player.queue = items
  player.index = -1
  rebuildOrder(false)
  playIndex(start)
}

/** Reemplaza la cola sin interrumpir lo que suena (p. ej. al refrescar la biblioteca). */
export function syncQueueKeepingCurrent(items: QueueItem[]): void {
  const cur = current()
  player.queue = items
  const at = cur ? items.findIndex((q) => q.track.path === cur.track.path) : -1
  player.index = at
  rebuildOrder(true)
}

// Los navegadores solo dejan sonar un <audio> si su primer play() ocurre dentro de un gesto del usuario. Como al
// cargar por el backend el play() llega después de varios await, se "desbloquea" el elemento con un silencio.
const SILENT = 'data:audio/wav;base64,UklGRiQAAABXQVZFZm10IBAAAAABAAEARKwAAIhYAQACABAAZGF0YQAAAAA='
let primed = false
function prime(a: HTMLAudioElement) {
  if (primed) return
  primed = true
  a.src = SILENT
  void a.play().catch(() => undefined)
}

/** Calienta el audio en el primer clic de la sesión (crear el AudioContext es lo que más tarda la primera vez). */
export function warmUp(): void {
  const go = () => {
    prime(ensureAudio())
    ensureGraph()
    void ctx?.resume()
  }
  window.addEventListener('pointerdown', go, { once: true, capture: true })
}

export function playIndex(i: number): void {
  if (i < 0 || i >= player.queue.length) return
  t0 = performance.now()
  player.log = []
  mark(`clic: ${player.queue[i].track.title}`)
  const a = ensureAudio()
  prime(a)
  ensureGraph()
  void ctx?.resume()
  player.index = i
  player.time = 0
  player.duration = 0
  player.error = ''
  player.loading = true
  player.loadPct = 0
  player.analysis = null
  player.key = null
  player.bpm = 0
  player.analyzing = false
  beatState.locked = false
  beatState.intervals = []
  const token = ++playToken
  clearTimeout(stallTimer)
  const it = player.queue[i]
  void analysisFromDisk(it).then((a) => {
    if (a && token === playToken && !player.analysis) {
      mark('análisis desde disco')
      analysisCache.set(it.track.path, a)
      applyAnalysis(a, token)
    }
  })

  // Análisis y precarga de la siguiente esperan a que el audio lleve un rato estable: así no compiten por CPU
  // con el arranque (eso era lo que hacía que la primera canción se atropellara).
  const started = () => {
    whenStable(token, () => {
      void startAnalysis(it, token)
      const n = order.length ? order[(order.indexOf(player.index) + 1) % order.length] : -1
      if (n >= 0 && n !== player.index) prefetch(player.queue[n]?.track)
    })
  }

  if (directPending && !flags.directFailed) {
    // todavía se está comprobando si el webview carga audio por HTTP: se espera ese veredicto (máx. 2,5 s)
    mark('esperando comprobación de audio directo')
    void directPending.then(() => {
      if (token === playToken) startPlayback(a, it, token, started)
    })
    return
  }
  startPlayback(a, it, token, started)
}

function startPlayback(a: HTMLAudioElement, it: QueueItem, token: number, started: () => void): void {
  const cached = bytesCache.get(it.track.path)
  if (cached) {
    // ya está en memoria: sonido inmediato, sin red ni espera
    mark('desde memoria')
    a.src = toBlobUrl(cached, it.track.ext)
    void a.play().then(started, () => undefined)
    return
  }
  if (forceFallback || flags.directFailed) {
    void loadViaBackend(token).then(() => started())
    return
  }
  mark('reproducción directa por HTTP')
  a.src = it.url
  void a.play().then(started, (e: unknown) => {
    if ((e as Error)?.name !== 'AbortError') void loadViaBackend(token).then(() => started())
  })
  // Algunos webviews no emiten error: simplemente nunca cargan. Si a los 3 s no hay datos, se usa el respaldo.
  stallTimer = setTimeout(() => {
    if (token === playToken && a.readyState < 2 && a.src !== blobUrl) void loadViaBackend(token).then(() => started())
  }, 3000)
}

export function toggle(): void {
  if (!audio || player.index < 0) {
    if (player.queue.length) playIndex(0)
    return
  }
  ensureGraph()
  void ctx?.resume()
  if (audio.paused) void audio.play().catch(() => undefined)
  else audio.pause()
}

export function stop(): void {
  if (!audio) return
  audio.pause()
  audio.currentTime = 0
  player.time = 0
}

function step(dir: 1 | -1, auto: boolean): void {
  const n = player.queue.length
  if (!n) return
  if (auto && player.loop === 'one') {
    playIndex(player.index)
    return
  }
  if (!order.length) rebuildOrder(true)
  const pos = order.indexOf(player.index)
  let np = pos + dir
  if (np >= n || np < 0) {
    if (auto && player.loop === 'off') {
      stop()
      return
    }
    np = (np + n) % n
  }
  playIndex(order[np])
}

export const next = (auto = false): void => step(1, auto)

export function prev(): void {
  if (audio && audio.currentTime > 3) {
    audio.currentTime = 0
    return
  }
  step(-1, false)
}

export function seek(sec: number): void {
  if (!audio || !player.duration) return
  audio.currentTime = Math.max(0, Math.min(player.duration, sec))
  player.time = audio.currentTime
}

export function setVolume(v: number): void {
  player.volume = Math.max(0, Math.min(1, v))
  if (master) master.gain.value = player.volume
}

export function setEq(i: number, db: number): void {
  player.eq[i] = Math.max(-EQ_RANGE, Math.min(EQ_RANGE, db))
  applyEq()
}

export function resetEq(): void {
  player.eq.fill(0)
  applyEq()
}

export function setEqOn(on: boolean): void {
  player.eqOn = on
  applyEq()
}

function applyEq(): void {
  filters.forEach((f, i) => (f.gain.value = player.eqOn ? player.eq[i] : 0))
}

export function setShuffle(on: boolean): void {
  player.shuffle = on
  rebuildOrder(true)
}

export function cycleLoop(): void {
  player.loop = player.loop === 'off' ? 'all' : player.loop === 'all' ? 'one' : 'off'
}

export function formatInfo(): { kbps: number; khz: number } {
  const it = current()
  const khz = ctx ? Math.round(ctx.sampleRate / 1000) : 0
  const kbps = it && player.duration > 0 ? Math.round((it.track.size * 8) / player.duration / 1000) : 0
  return { kbps, khz }
}

function avg(buf: Uint8Array, a: number, b: number): number {
  let s = 0
  for (let i = a; i < b; i++) s += buf[i]
  return s / Math.max(1, b - a) / 255
}

/** Lee el analizador y devuelve el cuadro de datos para los visualizadores (objeto reutilizado). */
export function getFrame(now: number, dt: number): Frame {
  frame.time = now
  frame.dt = dt
  const live = !!analyser && !!audio && !audio.paused && !audio.ended
  if (analyser && live) {
    analyser.getByteFrequencyData(freqBuf)
    analyser.getByteTimeDomainData(waveBuf)
    frame.freq = freqBuf
    frame.wave = waveBuf
    const n = freqBuf.length // 1024 bins, ~21.5 Hz cada uno a 44.1 kHz
    frame.bass = avg(freqBuf, 1, 8)
    frame.mid = avg(freqBuf, 8, 90)
    frame.treble = avg(freqBuf, 90, Math.floor(n * 0.7))
    frame.level = Math.min(1, frame.bass * 0.5 + frame.mid * 0.35 + frame.treble * 0.15 + 0.1 * frame.bass)
    // Beat: el bajo supera su media móvil y ha pasado un mínimo desde el golpe anterior.
    bassAvg += (frame.bass - bassAvg) * Math.min(1, dt * 1.6)
    if (frame.bass > bassAvg * 1.28 + 0.06 && now - lastBeat > 0.22) {
      frame.beat = 1
      const gap = now - lastBeat
      if (gap > 0.28 && gap < 1.1) {
        beatState.intervals.push(gap)
        if (beatState.intervals.length > 16) beatState.intervals.shift()
        const sorted = [...beatState.intervals].sort((a, b) => a - b)
        let bpm = 60 / sorted[Math.floor(sorted.length / 2)]
        while (bpm < 80) bpm *= 2
        while (bpm > 170) bpm /= 2
        if (!beatState.locked && beatState.intervals.length >= 4) beatState.bpm = bpm
      }
      lastBeat = now
      beatState.last = now
      beatState.count++
    }
    frame.active = frame.level > 0.003
  } else {
    frame.bass *= 0.9
    frame.mid *= 0.9
    frame.treble *= 0.9
    frame.level *= 0.9
    frame.active = false
  }
  frame.beat = Math.max(0, frame.beat - dt * 3.2)
  return frame
}

export function libraryToQueue(pl: LibPlaylist, urlOf: (t: LibTrack) => string): QueueItem[] {
  return pl.tracks.map((t) => ({ track: t, playlist: pl.name, url: urlOf(t) }))
}

/** Comprueba una sola vez si el webview carga audio por HTTP; si no, se usa el respaldo desde el primer play. */
let directChecked = flags.directFailed
export async function checkDirect(url: string): Promise<void> {
  if (directChecked || forceFallback) {
    if (forceFallback) flags.directFailed = true
    return
  }
  directChecked = true
  let finish!: () => void
  directPending = new Promise<void>((r) => (finish = r))
  const ok = await new Promise<boolean>((resolve) => {
    const a = new Audio()
    a.preload = 'metadata'
    const done = (v: boolean) => {
      a.removeAttribute('src')
      a.load()
      resolve(v)
    }
    a.addEventListener('loadedmetadata', () => done(true), { once: true })
    a.addEventListener('error', () => done(false), { once: true })
    setTimeout(() => done(false), 2500)
    a.src = url
  })
  if (!ok) flags.directFailed = true
  directPending = null
  finish()
}

/** Duración de un archivo leyendo solo sus metadatos (no reproduce nada). */
export function probeDuration(url: string): Promise<number> {
  return new Promise((resolve) => {
    const a = new Audio()
    a.preload = 'metadata'
    const done = (v: number) => {
      a.removeAttribute('src')
      a.load()
      resolve(v)
    }
    a.addEventListener('loadedmetadata', () => done(Number.isFinite(a.duration) ? a.duration : 0), { once: true })
    a.addEventListener('error', () => done(0), { once: true })
    setTimeout(() => done(0), 8000)
    a.src = url
  })
}
