import { computed, reactive, watch } from 'vue'
import { api } from './api'
import { errorText } from './format'
import { checkDirect, current, flags, prefetch, warmUp, libraryToQueue, player, probeDuration, syncQueueKeepingCurrent } from './player/engine'
import { loadMetas } from './player/trackmeta'
import type { Job, LibPlaylist, LibraryResult, LibTrack, Settings, ToolStatus } from './types'
import { ACTIVE } from './types'

export type Tab = 'fetch' | 'queue' | 'player' | 'map' | 'export' | 'config'

export const state = reactive({
  tab: 'fetch' as Tab,
  jobs: [] as Job[],
  settings: null as Settings | null,
  status: null as ToolStatus | null,
  toast: '',
  library: { root: '', baseUrl: '', playlists: [] } as LibraryResult,
  libraryLoaded: false,
  libraryLoading: false,
  /** Duración por ruta relativa, rellenada en segundo plano leyendo metadatos. */
  durations: {} as Record<string, number>,
  /** Playlist seleccionada en la vista del reproductor (carpeta). */
  selected: '',
})

export const activeCount = computed(() => state.jobs.filter((j) => ACTIVE.includes(j.status)).length)
export const failedCount = computed(() => state.jobs.filter((j) => j.status === 'failed').length)
export const finishedCount = computed(() => state.jobs.filter((j) => !ACTIVE.includes(j.status)).length)

let toastTimer: ReturnType<typeof setTimeout> | undefined

export function toast(msg: string, ms = 4000): void {
  state.toast = msg
  clearTimeout(toastTimer)
  toastTimer = setTimeout(() => (state.toast = ''), ms)
}

/** Ejecuta una acción del backend mostrando el error en un aviso si falla. */
export async function guarded<T>(fn: () => Promise<T>): Promise<T | undefined> {
  try {
    return await fn()
  } catch (e) {
    toast(errorText(e))
    return undefined
  }
}

export function trackUrl(t: LibTrack): string {
  const parts = t.path.split('/').map(encodeURIComponent)
  return `${state.library.baseUrl}/f/${parts.join('/')}`
}

export const selectedPlaylist = computed<LibPlaylist | null>(
  () => state.library.playlists.find((p) => p.id === state.selected) ?? state.library.playlists[0] ?? null,
)

let probing = 0

async function probeDurations(): Promise<void> {
  const run = ++probing
  const todo = state.library.playlists.flatMap((p) => p.tracks).filter((t) => !(t.path in state.durations))
  if (todo.length) {
    await checkDirect(trackUrl(todo[0]))
    prefetch(selectedPlaylist.value?.tracks[0])
  }
  let i = 0
  const worker = async () => {
    while (i < todo.length && run === probing && !flags.directFailed) {
      const t = todo[i++]
      state.durations[t.path] = await probeDuration(trackUrl(t))
    }
  }
  await Promise.all([worker(), worker(), worker()])
}

export async function loadLibrary(): Promise<void> {
  if (state.libraryLoading) return
  state.libraryLoading = true
  try {
    const lib = await api.library()
    state.library = { root: lib.root, baseUrl: lib.baseUrl, playlists: lib.playlists ?? [] }
    state.libraryLoaded = true
    if (!state.library.playlists.some((p) => p.id === state.selected)) state.selected = state.library.playlists[0]?.id ?? ''
    // Si la playlist que suena sigue existiendo, se actualiza su cola sin cortar la canción.
    const playingName = player.queue[0]?.playlist
    const same = state.library.playlists.find((p) => p.name === playingName)
    if (same) syncQueueKeepingCurrent(libraryToQueue(same, trackUrl))
    void probeDurations()
  } catch (e) {
    toast(errorText(e))
  } finally {
    state.libraryLoading = false
  }
}

export async function init(): Promise<void> {
  warmUp()
  api.onQueue((jobs) => (state.jobs = jobs ?? []))
  api.onStatus((s) => (state.status = s))
  const [settings, jobs, status] = await Promise.all([api.getSettings(), api.queue(), api.status()])
  state.settings = settings
  state.jobs = jobs ?? []
  state.status = status
  await loadMetas()
  void loadLibrary()

  // La duración real se aprende al reproducir (sirve aunque el sondeo de metadatos no funcione).
  watch(
    () => player.duration,
    (d) => {
      const c = current()
      if (c && d > 0) state.durations[c.track.path] = d
    },
  )

  // Cuando termina una descarga la biblioteca cambia: se refresca (con un pequeño retraso para agrupar).
  let doneCount = state.jobs.filter((j) => j.status === 'done').length
  let timer: ReturnType<typeof setTimeout> | undefined
  watch(
    () => state.jobs.filter((j) => j.status === 'done').length,
    (n) => {
      if (n > doneCount) {
        clearTimeout(timer)
        timer = setTimeout(() => void loadLibrary(), 1500)
      }
      doneCount = n
    },
  )
}
