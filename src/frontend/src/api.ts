import type { ConvertProgress, ConvertRequest, ConvertResult, Job, LibraryResult, NewJob, PreviewResult, Settings, ToolStatus, TrackMeta, Loudness, NormalizeResult, SetResult, USBInfo, USBPreview, USBRequest, USBStatus, USBVolume } from './types'
import { ACTIVE } from './types'

// Puente hacia el backend en Go. Dentro de Wails, `window.go.<paquete>.App.*` y `window.runtime`
// los inyecta el propio Wails. Fuera de Wails (npm run dev en el navegador) se usa un simulador
// para poder trabajar en la interfaz sin compilar la app.

export interface Api {
  getSettings(): Promise<Settings>
  saveSettings(s: Settings): Promise<Settings>
  status(): Promise<ToolStatus>
  queue(): Promise<Job[]>
  library(): Promise<LibraryResult>
  /** Trozo de un archivo en base64. */
  readTrackChunk(path: string, offset: number, length: number): Promise<string>
  trackMeta(): Promise<Record<string, TrackMeta>>
  saveTrackMeta(path: string, meta: TrackMeta): Promise<void>
  /** Forma de onda en base64 (3 bytes por cuadro). */
  saveWave(path: string, size: number, b64: string): Promise<void>
  loadWave(path: string, size: number): Promise<string>
  trashTrack(path: string): Promise<void>
  createPlaylist(name: string): Promise<string>
  moveTrack(path: string, destDir: string): Promise<string>
  previewUrls(urls: string[]): Promise<PreviewResult[]>
  enqueue(items: NewJob[]): Promise<number>
  cancelJob(id: string): Promise<void>
  retryJob(id: string): Promise<boolean>
  retryFailed(): Promise<number>
  removeJob(id: string): Promise<void>
  clearFinished(): Promise<void>
  chooseFolder(): Promise<string>
  chooseFFmpeg(): Promise<string>
  openDownloads(): Promise<void>
  copyText(text: string): Promise<void>
  revealFile(path: string): Promise<void>
  updateTools(): Promise<string>
  spotifyTest(): Promise<string>
  spotifyLogin(): Promise<string>
  spotifyLogout(): Promise<void>
  toggleFullscreen(): Promise<boolean>
  copyTrack(path: string, destDir: string): Promise<string>
  createPlaylistFromSet(name: string, paths: string[]): Promise<SetResult>
  prepareAnalysis(path: string): Promise<number>
  readAnalysisChunk(path: string, offset: number, length: number): Promise<string>
  releaseAnalysis(path: string): Promise<void>
  measureLoudness(path: string): Promise<Loudness>
  normalizeTrack(path: string, refPath: string): Promise<NormalizeResult>
  usbVolumes(): Promise<USBVolume[]>
  usbInspect(dest: string): Promise<USBInfo>
  usbPreview(req: USBRequest): Promise<USBPreview>
  usbState(): Promise<USBStatus>
  startUsbExport(req: USBRequest): Promise<void>
  cancelUsbExport(): Promise<void>
  convertTracks(req: ConvertRequest): Promise<ConvertResult>
  cancelConvert(): Promise<void>
  onConvert(cb: (p: ConvertProgress) => void): () => void
  onUsb(cb: (s: USBStatus) => void): () => void
  onQueue(cb: (jobs: Job[]) => void): () => void
  onStatus(cb: (s: ToolStatus) => void): () => void
}

type Bound = Record<string, (...args: unknown[]) => Promise<unknown>>

declare global {
  interface Window {
    go?: Record<string, { App?: Bound } | undefined>
    runtime?: { EventsOn: (name: string, cb: (...data: unknown[]) => void) => () => void }
    /** Solo para pruebas fuera de Wails: biblioteca simulada con archivos reales servidos por HTTP. */
    __MOCK_LIB__?: LibraryResult
  }
}

// Wails publica los métodos bajo el nombre del paquete Go que contiene App (hoy `backend`, antes `main`):
// se busca el paquete que la tenga en vez de fijar el nombre.
function boundApp(): Bound | undefined {
  const go = window.go
  if (!go) return undefined
  return go.backend?.App ?? go.main?.App ?? Object.values(go).find((pkg) => pkg?.App)?.App
}

export const inWails = (): boolean => !!boundApp()

const app = (): Bound => boundApp()!
const call = <T>(name: string, ...args: unknown[]): Promise<T> => app()[name](...args) as Promise<T>

const real: Api = {
  getSettings: () => call('GetSettings'),
  saveSettings: (s) => call('SaveSettings', s),
  status: () => call('Status'),
  queue: () => call('Queue'),
  library: () => call('Library'),
  readTrackChunk: (path, offset, length) => call('ReadTrackChunk', path, offset, length),
  trackMeta: async () => (await call<Record<string, TrackMeta> | null>('TrackMeta')) ?? {},
  saveTrackMeta: (path, meta) => call('SaveTrackMeta', path, meta),
  saveWave: (path, size, b64) => call('SaveWave', path, size, b64),
  loadWave: async (path, size) => (await call<string | null>('LoadWave', path, size)) ?? '',
  trashTrack: (path) => call('TrashTrack', path),
  createPlaylist: (name) => call('CreatePlaylist', name),
  moveTrack: (path, destDir) => call('MoveTrack', path, destDir),
  previewUrls: (urls) => call('PreviewURLs', urls),
  enqueue: (items) => call('Enqueue', items),
  cancelJob: (id) => call('CancelJob', id),
  retryJob: (id) => call('RetryJob', id),
  retryFailed: () => call('RetryFailed'),
  removeJob: (id) => call('RemoveJob', id),
  clearFinished: () => call('ClearFinished'),
  chooseFolder: () => call('ChooseFolder'),
  chooseFFmpeg: () => call('ChooseFFmpeg'),
  openDownloads: () => call('OpenDownloads'),
  copyText: (text) => call('CopyText', text),
  revealFile: (path) => call('RevealFile', path),
  updateTools: () => call('UpdateTools'),
  spotifyTest: () => call('SpotifyTest'),
  spotifyLogin: () => call('SpotifyLogin'),
  spotifyLogout: () => call('SpotifyLogout'),
  toggleFullscreen: () => call('ToggleFullscreen'),
  copyTrack: (path, destDir) => call('CopyTrack', path, destDir),
  createPlaylistFromSet: (name, paths) => call('CreatePlaylistFromSet', name, paths),
  prepareAnalysis: (path) => call('PrepareAnalysis', path),
  readAnalysisChunk: (path, offset, length) => call('ReadAnalysisChunk', path, offset, length),
  releaseAnalysis: (path) => call('ReleaseAnalysis', path),
  measureLoudness: (path) => call('MeasureLoudness', path),
  normalizeTrack: (path, ref) => call('NormalizeTrack', path, ref),
  usbVolumes: () => call('USBVolumes'),
  usbInspect: (dest) => call('USBInspect', dest),
  usbPreview: (req) => call('USBPreview', req),
  usbState: () => call('USBState'),
  startUsbExport: (req) => call('StartUSBExport', req),
  cancelUsbExport: () => call('CancelUSBExport'),
  convertTracks: (req) => call('ConvertTracks', req),
  cancelConvert: () => call('CancelConvert'),
  onConvert: (cb) => window.runtime!.EventsOn('convert', (d) => cb(d as ConvertProgress)),
  onUsb: (cb) => window.runtime!.EventsOn('usb', (d) => cb(d as USBStatus)),
  onQueue: (cb) => window.runtime!.EventsOn('queue', (d) => cb(d as Job[])),
  onStatus: (cb) => window.runtime!.EventsOn('status', (d) => cb(d as ToolStatus)),
}

// ---------------------------------------------------------------------------
// Simulador para desarrollo en el navegador
// ---------------------------------------------------------------------------

function mockLibrary(): LibraryResult {
  if (window.__MOCK_LIB__) return window.__MOCK_LIB__
  const mk = (folder: string, names: string[]) => ({
    id: folder,
    name: folder,
    path: folder,
    cover: '',
    tracks: names.map((n, i) => ({
      id: `${folder}-${i}`,
      title: n,
      path: `${folder}/${String(i + 1).padStart(2, '0')} - ${n}.mp3`,
      ext: 'mp3',
      size: 4_000_000,
    })),
  })
  return {
    root: '/Users/tú/Downloads',
    baseUrl: 'http://127.0.0.1:1/demo',
    playlists: [
      mk('kawaii 17', ['Llama Whippin Intro', 'Heroines', 'We Are Going To Eclecfunk Your Ass', 'Seventeen']),
      mk('night drive', ['Neon Rain', 'Afterglow', 'Chrome Hearts']),
    ],
  }
}

const mockMeta: Record<string, TrackMeta> = {}
const mockWaves: Record<string, string> = {}
const mockPcm: Record<string, Uint8Array> = {}

function createMock(): Api {
  let settings: Settings = {
    outDir: '/Users/tú/Downloads',
    format: 'mp3',
    quality: '0',
    concurrency: 2,
    ffmpegPath: '',
    autoUpdate: true,
    character: 'miku',
    cookiesBrowser: '',
    keyNotation: 'camelot',
    spotifyClientId: '',
    spotifyClientSecret: '',
    spotifyRefreshToken: '',
    spotifyUser: '',
  }
  const status: ToolStatus = {
    ytDlpPath: '/Users/tú/Library/Application Support/Cyberia/bin/yt-dlp',
    ytDlpVersion: '2026.08.19',
    ytDlpManaged: true,
    ffmpegPath: '/opt/homebrew/bin/ffmpeg',
    ffmpegOk: true,
    busy: false,
    message: '',
    platform: 'darwin',
    redirectUri: 'http://127.0.0.1:8888/callback',
  }
  let jobs: Job[] = []
  const queueSubs = new Set<(j: Job[]) => void>()
  const statusSubs = new Set<(s: ToolStatus) => void>()
  const usbSubs = new Set<(s: USBStatus) => void>()
  const convSubs = new Set<(p: ConvertProgress) => void>()
  let convCancel = false
  const emit = () => queueSubs.forEach((cb) => cb(jobs.map((j) => ({ ...j }))))
  let seq = 0

  setInterval(() => {
    const running = jobs.filter((j) => j.status === 'downloading' || j.status === 'converting')
    for (const j of jobs.filter((x) => x.status === 'queued').slice(0, Math.max(0, settings.concurrency - running.length))) {
      j.status = 'downloading'
    }
    for (const j of jobs) {
      if (j.status === 'downloading') {
        j.percent = Math.min(100, j.percent + 6 + Math.random() * 10)
        j.speed = 800_000 + Math.random() * 900_000
        j.eta = Math.max(0, Math.round((100 - j.percent) / 12))
        if (j.percent >= 100) j.status = 'converting'
      } else if (j.status === 'converting') {
        if (j.title.includes('falla')) {
          j.status = 'failed'
          j.error = 'sin coincidencia clara en YouTube (simulado)'
        } else {
          j.status = 'done'
          j.filePath = `${settings.outDir}/${j.folder}/${j.title}.mp3`
        }
        j.speed = 0
        j.eta = -1
      }
    }
    emit()
  }, 500)

  return {
    getSettings: async () => ({ ...settings }),
    saveSettings: async (s) => ((settings = { ...s, spotifyUser: settings.spotifyUser }), { ...settings }),
    status: async () => ({ ...status }),
    queue: async () => jobs.map((j) => ({ ...j })),
    library: async () => mockLibrary(),
    trackMeta: async () => JSON.parse(JSON.stringify(mockMeta)),
    saveTrackMeta: async (path, meta) => {
      mockMeta[path] = JSON.parse(JSON.stringify(meta))
    },
    saveWave: async (path, size, b64) => {
      mockWaves[`${path}:${size}`] = b64
    },
    loadWave: async (path, size) => mockWaves[`${path}:${size}`] ?? '',
    readTrackChunk: async (path, offset, length) => {
      const lib = mockLibrary()
      const url = `${lib.baseUrl}/f/${path.split('/').map(encodeURIComponent).join('/')}`
      const r = await fetch(url, { headers: { Range: `bytes=${offset}-${offset + length - 1}` } })
      const bytes = new Uint8Array(await r.arrayBuffer())
      let bin = ''
      for (let i = 0; i < bytes.length; i += 0x8000) bin += String.fromCharCode(...bytes.subarray(i, i + 0x8000))
      return btoa(bin)
    },
    trashTrack: async () => undefined,
    createPlaylist: async (name) => name,
    moveTrack: async (path, destDir) => `${destDir}/${path.split('/').pop()}`,
    copyTrack: async (path, destDir) => `${destDir}/${path.split('/').pop()}`,
    createPlaylistFromSet: async (name, paths) => ({ folder: name, copied: paths.length, skipped: null }),
    // Simulador: decodifica en el navegador para poder probar el flujo de WAV/FLAC sin backend.
    prepareAnalysis: async (path) => {
      const lib = mockLibrary()
      const url = `${lib.baseUrl}/f/${path.split('/').map(encodeURIComponent).join('/')}`
      const data = await (await fetch(url)).arrayBuffer()
      const ac = new OfflineAudioContext(1, 1, 22050)
      const buf = await ac.decodeAudioData(data)
      const ch = buf.getChannelData(0)
      const pcm = new Int16Array(ch.length)
      for (let i = 0; i < ch.length; i++) pcm[i] = Math.max(-1, Math.min(1, ch[i])) * 32767
      mockPcm[path] = new Uint8Array(pcm.buffer)
      return pcm.byteLength
    },
    readAnalysisChunk: async (path, offset, length) => {
      const b = mockPcm[path].subarray(offset, offset + length)
      let bin = ''
      for (let i = 0; i < b.length; i += 0x8000) bin += String.fromCharCode(...b.subarray(i, i + 0x8000))
      return btoa(bin)
    },
    releaseAnalysis: async (path) => void delete mockPcm[path],
    previewUrls: async (urls) => {
      await new Promise((r) => setTimeout(r, 500))
      return urls.map((url) => {
        if (url.includes('mala')) return { url, preview: null, error: 'Unsupported URL (simulado)' }
        const spotify = url.includes('spotify')
        return {
          url,
          error: '',
          preview: {
            source: spotify ? 'spotify' : 'web',
            sourceUrl: url,
            title: spotify ? 'Mi playlist' : 'kawaii 17',
            uploader: spotify ? 'Santi' : 'carolina-melon',
            isPlaylist: true,
            entries: [1, 2, 3, 4].map((i) => ({
              id: String(i),
              title: i === 3 ? `Canción ${i} (falla)` : `Canción ${i}`,
              uploader: spotify ? 'Artista X' : 'carolina-melon',
              url: `${url}#${i}`,
              thumbnail: '',
              duration: 150 + i * 23,
              index: i,
            })),
          },
        }
      })
    },
    enqueue: async (items) => {
      for (const it of items) {
        jobs.push({
          search: false, duration: 0, outName: '', ...it, id: `mock${++seq}`, status: 'queued', percent: 0, speed: 0,
          eta: -1, error: '', filePath: '', addedAt: new Date().toISOString(),
        })
      }
      emit()
      return items.length
    },
    cancelJob: async (id) => {
      const j = jobs.find((x) => x.id === id)
      if (j && ACTIVE.includes(j.status)) j.status = 'canceled'
      emit()
    },
    retryJob: async (id) => {
      const j = jobs.find((x) => x.id === id)
      if (!j) return false
      Object.assign(j, { status: 'queued', percent: 0, error: '' })
      emit()
      return true
    },
    retryFailed: async () => {
      const failed = jobs.filter((j) => j.status === 'failed')
      failed.forEach((j) => Object.assign(j, { status: 'queued', percent: 0, error: '' }))
      emit()
      return failed.length
    },
    removeJob: async (id) => ((jobs = jobs.filter((j) => j.id !== id || ACTIVE.includes(j.status))), emit()),
    clearFinished: async () => ((jobs = jobs.filter((j) => ACTIVE.includes(j.status))), emit()),
    chooseFolder: async () => '/Users/tú/Music/Nueva carpeta',
    chooseFFmpeg: async () => '/opt/homebrew/bin/ffmpeg',
    openDownloads: async () => undefined,
    copyText: async (text) => {
      await navigator.clipboard?.writeText(text)
    },
    revealFile: async () => undefined,
    updateTools: async () => {
      statusSubs.forEach((cb) => cb({ ...status, busy: true, message: 'Actualizando yt-dlp…' }))
      await new Promise((r) => setTimeout(r, 900))
      statusSubs.forEach((cb) => cb({ ...status }))
      return 'yt-dlp está actualizado (simulado)'
    },
    spotifyTest: async () => 'Credenciales correctas (simulado)',
    spotifyLogin: async () => {
      await new Promise((r) => setTimeout(r, 700))
      settings = { ...settings, spotifyUser: 'Santi' }
      return 'Santi'
    },
    spotifyLogout: async () => void (settings = { ...settings, spotifyUser: '' }),
    toggleFullscreen: async () => {
      if (document.fullscreenElement) await document.exitFullscreen()
      else await document.documentElement.requestFullscreen().catch(() => undefined)
      return !!document.fullscreenElement
    },
    measureLoudness: async (path) => {
      await new Promise((r) => setTimeout(r, 500))
      return { lufs: path.length % 2 ? -21.4 : -9.6, peak: -1.2 }
    },
    normalizeTrack: async (path) => {
      await new Promise((r) => setTimeout(r, 1500))
      return { rel: path, gainDb: 11.8, before: -21.4, after: -9.7, reference: -9.6, peak: -0.6, limited: false, newSize: 5_000_000, oldSize: 4_000_000, backup: '/tmp/backup.mp3' }
    },
    usbVolumes: async () => [
      { name: 'PINK', path: '/Volumes/PINK', total: 64e9, free: 21.4e9, hasPioneer: true },
      { name: 'DJ-USB', path: '/Volumes/DJ-USB', total: 32e9, free: 31.9e9, hasPioneer: false },
    ],
    usbInspect: async (dest) => ({
      exists: !!dest, hasLibrary: dest.includes('PINK'), hasOneLibrary: false, total: 64e9, free: 21.4e9, readError: '',
      playlists: dest.includes('PINK')
        ? [{ id: 1, name: 'House 2024', parent: '', folder: false, count: 42 }, { id: 2, name: 'Warmup', parent: '', folder: false, count: 18 }, { id: 3, name: 'Sets', parent: '', folder: true, count: 0 }, { id: 4, name: 'Closing', parent: 'Sets', folder: false, count: 9 }]
        : [],
    }),
    usbPreview: async (req) => {
      const lists = req.jobs.map((j) => ({
        name: j.name || (j.existingId === 1 ? 'House 2024' : 'Warmup'),
        existing: j.existingId !== 0,
        inList: 0,
        tracks: j.tracks.map((t, i) => ({ title: t.replace(/\.[^.]+$/, '').split('/').pop() ?? t, copy: i % 3 !== 0, size: 6_500_000 })),
      }))
      const copy = lists.reduce((n, l) => n + l.tracks.filter((t) => t.copy).length, 0)
      return { lists, copyTracks: copy, copyBytes: copy * 6_500_000, unsupported: 0, free: 21.4e9 }
    },
    usbState: async () => ({ running: false, done: 0, total: 0, message: '', error: '' }),
    startUsbExport: async (req) => {
      const total = 6
      for (let i = 0; i <= total; i++) {
        await new Promise((r) => setTimeout(r, 250))
        const done = i === total
        usbSubs.forEach((cb) =>
          cb({
            running: !done,
            done: i,
            total,
            message: done ? 'Listo' : `Canción ${i + 1}.mp3`,
            error: '',
            report: done ? { root: 'PIONEER', tracks: total - 2, reused: 2, extended: 1, merged: true, playlists: req.jobs.length, analyzed: total - 1, bytes: 48_000_000, skipped: null, warnings: ['1 canciones no estaban analizadas en la app (simulado).'] } : null,
          }),
        )
      }
    },
    cancelUsbExport: async () => undefined,
    convertTracks: async (req) => {
      convCancel = false
      const res: ConvertResult = { converted: 0, trashed: 0, folder: req.mode === 'playlist' ? req.newName : '', skipped: [], failed: [], bytesBefore: 0, bytesAfter: 0, canceled: false }
      for (let i = 0; i < req.rels.length; i++) {
        if (convCancel) {
          res.canceled = true
          break
        }
        const name = req.rels[i].split('/').pop() ?? ''
        convSubs.forEach((cb) => cb({ done: i, total: req.rels.length, current: name }))
        await new Promise((r) => setTimeout(r, 250))
        if (name.toLowerCase().endsWith('.' + req.format)) res.skipped!.push(`${name}: ya está en ${req.format.toUpperCase()}`)
        else {
          res.converted++
          res.bytesBefore += 30_000_000
          res.bytesAfter += req.format === 'wav' || req.format === 'flac' ? 28_000_000 : 8_000_000
          if (req.mode === 'replace') res.trashed++
        }
      }
      convSubs.forEach((cb) => cb({ done: req.rels.length, total: req.rels.length, current: '' }))
      return res
    },
    cancelConvert: async () => void (convCancel = true),
    onConvert: (cb) => (convSubs.add(cb), () => void convSubs.delete(cb)),
    onUsb: (cb) => (usbSubs.add(cb), () => void usbSubs.delete(cb)),
    onQueue: (cb) => (queueSubs.add(cb), () => void queueSubs.delete(cb)),
    onStatus: (cb) => (statusSubs.add(cb), () => void statusSubs.delete(cb)),
  }
}

let mock: Api | null = null

// Se decide en cada llamada (no al cargar el módulo) por si Wails inyecta `window.go` un instante después.
export const api: Api = new Proxy({} as Api, {
  get(_t, prop: string) {
    if (inWails()) return real[prop as keyof Api]
    mock ??= createMock()
    return mock[prop as keyof Api]
  },
})
