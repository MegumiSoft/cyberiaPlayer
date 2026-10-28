package backend

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"musicdl/src/backend/convert"
	"musicdl/src/backend/library"
	"musicdl/src/backend/trash"
	"musicdl/src/backend/ytdlp"
)

// ConvertRequest pide convertir canciones de la biblioteca a otro formato.
type ConvertRequest struct {
	Rels    []string `json:"rels"`    // canciones (ruta relativa) a convertir
	Format  string   `json:"format"`  // mp3, m4a, opus, aac, flac, wav
	Bitrate *int     `json:"bitrate"` // kbps de tasa fija; null = el predeterminado; se ignora en flac/wav
	VBR     *int     `json:"vbr"`     // calidad VBR (solo MP3: 0 = la mejor); null = tasa fija
	// Mode: "keep" deja los originales y los nuevos al lado; "playlist" crea una playlist nueva COMPLETA (convierte
	// lo que hace falta y copia las que ya estaban en el formato); "replace" manda los originales a la papelera
	// cuando su copia convertida ya existe.
	Mode    string `json:"mode"`
	NewName string `json:"newName"` // nombre de la playlist nueva (modo "playlist")
}

// ConvertResult resume lo ocurrido.
type ConvertResult struct {
	Converted   int      `json:"converted"`
	Copied      int      `json:"copied"` // modo "playlist": las que ya estaban en el formato y se copiaron tal cual
	Trashed     int      `json:"trashed"`
	Folder      string   `json:"folder"` // playlist creada (modo "playlist")
	Skipped     []string `json:"skipped"`
	Failed      []string `json:"failed"`
	BytesBefore int64    `json:"bytesBefore"`
	BytesAfter  int64    `json:"bytesAfter"`
	Canceled    bool     `json:"canceled"`
}

// ConvertProgress se emite (evento «convert») tras cada canción.
type ConvertProgress struct {
	Done    int    `json:"done"`
	Total   int    `json:"total"`
	Current string `json:"current"`
}

// trashFile manda un archivo a la papelera del sistema; es una variable para que los tests no toquen la real.
var trashFile = trash.Move

// ConvertFormatsInfo es lo que la interfaz necesita para armar el diálogo de conversión.
type ConvertFormatsInfo struct {
	Formats   []convert.Format `json:"formats"`
	LossyExts []string         `json:"lossyExts"` // extensiones de origen con pérdida (sin punto)
}

// ConvertFormats lista los formatos de salida, sus calidades y qué extensiones son con pérdida.
func (a *App) ConvertFormats() ConvertFormatsInfo {
	return ConvertFormatsInfo{Formats: convert.Formats(), LossyExts: convert.LossyExts()}
}

var convState struct {
	mu     sync.Mutex
	cancel context.CancelFunc
}

// CancelConvert detiene la conversión en curso (lo ya convertido se conserva).
func (a *App) CancelConvert() {
	convState.mu.Lock()
	defer convState.mu.Unlock()
	if convState.cancel != nil {
		convState.cancel()
	}
}

func (a *App) emitConvert(p ConvertProgress) {
	if a.wails != nil {
		wruntime.EventsEmit(a.wails, "convert", p)
	}
}

// ConvertTracks convierte las canciones pedidas, una por una. Nunca sobrescribe nada: si el archivo de destino ya
// existe, esa canción se omite. Se conservan etiquetas, carátula, rejilla, cues y análisis.
func (a *App) ConvertTracks(req ConvertRequest) (ConvertResult, error) {
	var res ConvertResult
	f, ok := convert.Lookup(req.Format)
	if !ok {
		return res, fmt.Errorf("formato desconocido: %s", req.Format)
	}
	opts := convert.Options{Bitrate: req.Bitrate, VBR: req.VBR}
	if err := convert.Validate(req.Format, opts); err != nil {
		return res, err
	}
	if len(req.Rels) == 0 {
		return res, errors.New("no hay canciones que convertir")
	}
	switch req.Mode {
	case "keep", "playlist", "replace":
	default:
		return res, fmt.Errorf("modo desconocido: %q", req.Mode)
	}
	ff, ok := a.yt.FFmpeg()
	if !ok {
		return res, errors.New("ffmpeg no encontrado: instálalo o indica su ruta en CONFIG")
	}
	convState.mu.Lock()
	if convState.cancel != nil {
		convState.mu.Unlock()
		return res, errors.New("ya hay una conversión en curso")
	}
	ctx, cancel := context.WithCancel(a.ctx)
	convState.cancel = cancel
	convState.mu.Unlock()
	defer func() {
		cancel()
		convState.mu.Lock()
		convState.cancel = nil
		convState.mu.Unlock()
	}()

	root := a.settings.Get().OutDir
	runner := convert.Runner{FFmpeg: ff, Configure: ytdlp.ConfigureCmd}

	folder := ""
	if req.Mode == "playlist" {
		var err error
		if folder, err = library.CreateFolder(root, req.NewName); err != nil {
			return res, err
		}
		res.Folder = folder
	}

	total := len(req.Rels)
	for i, rel := range req.Rels {
		if ctx.Err() != nil {
			res.Canceled = true
			break
		}
		name := path.Base(rel)
		a.emitConvert(ConvertProgress{Done: i, Total: total, Current: name})
		a.convertOne(ctx, runner, f, req, opts, rel, folder, root, &res)
	}
	if ctx.Err() != nil {
		res.Canceled = true
	}
	a.emitConvert(ConvertProgress{Done: total, Total: total})
	if req.Mode == "playlist" && res.Converted+res.Copied == 0 {
		_ = os.Remove(filepath.Join(root, filepath.FromSlash(folder))) // carpeta vacía: no dejarla
		res.Folder = ""
	}
	return res, nil
}

func (a *App) convertOne(ctx context.Context, r convert.Runner, f convert.Format, req ConvertRequest, opts convert.Options, rel, folder, root string, res *ConvertResult) {
	name := path.Base(rel)
	src, err := library.ResolveFile(root, rel)
	if err != nil {
		res.Failed = append(res.Failed, name+": "+err.Error())
		return
	}
	if strings.EqualFold(path.Ext(rel), f.Ext) {
		if req.Mode == "playlist" {
			// La playlist nueva debe quedar completa: lo que ya está en el formato se copia tal cual (con sus cues y rejilla).
			nuevo, err := library.CopyTrack(root, rel, folder, "")
			if err != nil {
				res.Failed = append(res.Failed, name+": "+err.Error())
				return
			}
			_ = a.meta.Copy(rel, filepath.ToSlash(nuevo))
			res.Copied++
			return
		}
		res.Skipped = append(res.Skipped, name+": ya está en "+strings.ToUpper(strings.TrimPrefix(f.Ext, ".")))
		return
	}
	srcInfo, err := os.Stat(src)
	if err != nil {
		res.Failed = append(res.Failed, name+": "+err.Error())
		return
	}
	destDir := path.Dir(rel)
	if req.Mode == "playlist" {
		destDir = folder
	}
	destRel := strings.TrimSuffix(path.Base(rel), path.Ext(rel)) + f.Ext
	if destDir != "." && destDir != "" {
		destRel = path.Join(destDir, destRel)
	}
	dest := filepath.Join(root, filepath.FromSlash(destRel))
	if _, err := os.Lstat(dest); err == nil {
		res.Skipped = append(res.Skipped, name+": ya existe "+path.Base(destRel))
		return
	}
	if err := r.Convert(ctx, src, dest, req.Format, opts); err != nil {
		if ctx.Err() != nil {
			return
		}
		res.Failed = append(res.Failed, name+": "+err.Error())
		return
	}
	res.Converted++
	res.BytesBefore += srcInfo.Size()
	if st, err := os.Stat(dest); err == nil {
		res.BytesAfter += st.Size()
		// Los cues y la rejilla viajan con la canción; el análisis (BPM, tono, onda) sigue valiendo aunque cambie el formato.
		_ = a.meta.Copy(rel, destRel)
		_ = a.meta.Resize(destRel, srcInfo.Size(), st.Size())
	}
	if req.Mode == "replace" {
		if err := trashFile(src); err != nil {
			res.Failed = append(res.Failed, name+": se convirtió, pero el original no se pudo mandar a la papelera ("+err.Error()+")")
			return
		}
		_ = a.meta.Delete(rel)
		res.Trashed++
	}
}
