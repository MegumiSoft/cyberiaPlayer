// Tipos que reflejan el JSON que devuelve el backend en Go.

export type JobStatus = 'queued' | 'downloading' | 'converting' | 'done' | 'failed' | 'canceled'

export interface Job {
  id: string
  url: string
  title: string
  uploader: string
  thumbnail: string
  folder: string
  index: number
  search: boolean
  duration: number
  outName: string
  status: JobStatus
  percent: number
  speed: number
  eta: number
  error: string
  filePath: string
  addedAt: string
}

export interface NewJob {
  url: string
  title: string
  uploader: string
  thumbnail: string
  folder: string
  index: number
  search?: boolean
  duration?: number
  outName?: string
}

export interface Entry {
  id: string
  title: string
  uploader: string
  url: string
  thumbnail: string
  duration: number
  index: number
}

export interface Cue {
  /** 0-7 = hot cues A-H; -1 = memory cue */
  slot: number
  timeMs: number
  name: string
  color: number
}

export type ConvertFormat = 'mp3' | 'm4a' | 'opus' | 'aac' | 'flac' | 'wav'
export type ConvertMode = 'keep' | 'playlist' | 'replace'
export interface ConvertRequest {
  rels: string[]
  format: ConvertFormat
  /** kbps de tasa fija; null = el predeterminado del formato (se ignora en FLAC/WAV). */
  bitrate: number | null
  /** Calidad VBR (solo MP3; 0 = la mejor); null = tasa fija. */
  vbr: number | null
  mode: ConvertMode
  newName: string
}

/** Un formato de salida tal como lo describe el backend. */
export interface ConvertFormatInfo {
  id: ConvertFormat
  label: string
  lossy: boolean
  default: number
  bitrates: number[]
  vbr: number[]
}
export interface ConvertFormatsInfo {
  formats: ConvertFormatInfo[]
  /** Extensiones de origen con pérdida (sin punto). */
  lossyExts: string[]
}
export interface ConvertResult {
  converted: number
  /** Modo playlist: las que ya estaban en el formato y se copiaron tal cual. */
  copied: number
  trashed: number
  folder: string
  skipped: string[] | null
  failed: string[] | null
  bytesBefore: number
  bytesAfter: number
  canceled: boolean
}
export interface ConvertProgress {
  done: number
  total: number
  current: string
}

export interface StoredAnalysis {
  size: number
  version: number
  duration: number
  bpm: number
  bpmConf?: number
  bpmAlt?: number[]
  beatOffset: number
  keyPc: number
  keyMinor: boolean
  keyConf: number
  hasKey: boolean
}

/** Lo que se guarda por canción: análisis automático + ediciones del usuario. */
export interface TrackMeta {
  analysis?: StoredAnalysis | null
  gridSet: boolean
  gridBpm: number
  gridOffset: number
  cues: Cue[]
}

export interface Preview {
  source: 'web' | 'spotify' | string
  sourceUrl: string
  title: string
  uploader: string
  isPlaylist: boolean
  entries: Entry[]
  /** Motivo por el que yt-dlp falló cuando solo se pudo mostrar información básica. */
  warning?: string
}

export interface PreviewResult {
  url: string
  preview: Preview | null
  error: string
}

export interface Settings {
  outDir: string
  format: string
  quality: string
  concurrency: number
  ffmpegPath: string
  autoUpdate: boolean
  character: 'miku' | 'gir' | 'cat' | 'none' | string
  cookiesBrowser: string // '' = ninguno
  keyNotation: 'camelot' | 'note'
  spotifyClientId: string
  spotifyClientSecret: string
  spotifyRefreshToken: string // el backend nunca lo envía; existe para que el tipo coincida
  spotifyUser: string
}

export interface ToolStatus {
  ytDlpPath: string
  ytDlpVersion: string
  ytDlpManaged: boolean
  ffmpegPath: string
  ffmpegOk: boolean
  busy: boolean
  message: string
  platform: 'windows' | 'darwin' | 'linux' | string
  redirectUri: string
}

export interface LibTrack {
  id: string
  title: string
  path: string
  ext: string
  size: number
}

export interface LibPlaylist {
  id: string
  name: string
  path: string
  cover: string
  tracks: LibTrack[]
}

export interface LibraryResult {
  root: string
  baseUrl: string
  playlists: LibPlaylist[]
}

export const FORMATS = ['mp3', 'm4a', 'opus', 'flac', 'wav'] as const
export const LOSSLESS = ['flac', 'wav']
export const QUALITIES = [
  { value: '0', label: 'La mejor (VBR)' },
  { value: '320K', label: '320 kbps' },
  { value: '256K', label: '256 kbps' },
  { value: '192K', label: '192 kbps' },
  { value: '128K', label: '128 kbps' },
]

export const ACTIVE: JobStatus[] = ['queued', 'downloading', 'converting']

export interface USBVolume {
  name: string
  path: string
  total: number
  free: number
  hasPioneer: boolean
}
export interface USBPlaylist {
  id: number
  name: string
  parent: string
  folder: boolean
  count: number
}
export interface USBInfo {
  exists: boolean
  hasLibrary: boolean
  hasOneLibrary: boolean
  total: number
  free: number
  playlists: USBPlaylist[] | null
  readError: string
}
export interface USBJob {
  name: string
  existingId: number
  tracks: string[]
}
export interface USBRequest {
  dest: string
  deviceName: string
  jobs: USBJob[]
  waveform: 'blue' | 'rgb' | '3band'
}
export interface USBPreviewTrack {
  title: string
  copy: boolean
  size: number
}
export interface USBPreviewList {
  name: string
  existing: boolean
  tracks: USBPreviewTrack[] | null
  inList: number
}
export interface USBPreview {
  lists: USBPreviewList[] | null
  copyTracks: number
  copyBytes: number
  unsupported: number
  free: number
}
export interface USBReport {
  root: string
  tracks: number
  reused: number
  extended: number
  merged: boolean
  playlists: number
  analyzed: number
  bytes: number
  skipped: string[] | null
  warnings: string[] | null
}
export interface USBStatus {
  running: boolean
  done: number
  total: number
  message: string
  report?: USBReport | null
  error: string
}

export interface Loudness {
  lufs: number
  peak: number
}
export interface NormalizeResult {
  rel: string
  gainDb: number
  before: number
  after: number
  reference: number
  peak: number
  limited: boolean
  newSize: number
  oldSize: number
  backup: string
}

export interface SetResult {
  folder: string
  copied: number
  skipped: string[] | null
}
