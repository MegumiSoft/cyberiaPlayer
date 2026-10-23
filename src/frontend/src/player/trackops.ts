import { api } from '../api'
import { errorText } from '../format'
import { loadLibrary, state, toast } from '../store'
import type { LibPlaylist } from '../types'
import { renameMeta } from './trackmeta'

export const playlistName = (p: LibPlaylist | undefined): string => p?.name ?? '(sueltas)'

/** Mueve una canción y avisa: «título» movido de «origen» a «destino». */
export async function moveTrackTo(path: string, destPath: string, destName: string): Promise<void> {
  const from = state.library.playlists.find((p) => p.tracks.some((t) => t.path === path))
  const title = from?.tracks.find((t) => t.path === path)?.title ?? path
  if ((from?.path ?? '') === destPath) return
  try {
    const newPath = await api.moveTrack(path, destPath)
    if (newPath) renameMeta(path, newPath)
    toast(`«${title}» movido de «${playlistName(from)}» a «${destName}»`, 6000)
    await loadLibrary()
  } catch (e) {
    toast(errorText(e))
  }
}
