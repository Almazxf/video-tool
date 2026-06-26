package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

var (
	window fyne.Window

	modeSel       *widget.RadioGroup
	compressBox   *fyne.Container
	trimBox       *fyne.Container

	resModeSel    *widget.RadioGroup
	customWEntry  *widget.Entry
	customHEntry  *widget.Entry
	cropModeSel   *widget.RadioGroup
	formatSel     *widget.RadioGroup
	mirrorSel     *widget.RadioGroup
	crfSlider     *widget.Slider
	crfLabel      *widget.Label
	trimStartEntry *widget.Entry
	trimEndEntry  *widget.Entry
	progressBar   *widget.ProgressBar
	statusLabel   *widget.Label
	fileListLabel *widget.Label

	selectedFiles []string
)

var videoExts = map[string]bool{
	".mp4": true, ".mov": true, ".avi": true, ".mkv": true, ".webm": true,
	".wmv": true, ".flv": true, ".m4v": true, ".ogv": true,
}

func collectVideos(path string, out *[]string) {
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	if info.IsDir() {
		entries, err := os.ReadDir(path)
		if err != nil {
			return
		}
		for _, e := range entries {
			full := filepath.Join(path, e.Name())
			if e.IsDir() {
				collectVideos(full, out)
			} else if videoExts[strings.ToLower(filepath.Ext(e.Name()))] {
				*out = append(*out, full)
			}
		}
	} else if videoExts[strings.ToLower(filepath.Ext(path))] {
		*out = append(*out, path)
	}
}

func updateFileListLabel() {
	if len(selectedFiles) == 0 {
		fileListLabel.SetText("Файлы не выбраны. Перетащи видео/папку в это окно или нажми кнопку ниже.")
		return
	}
	fileListLabel.SetText(fmt.Sprintf("Выбрано файлов: %d", len(selectedFiles)))
}

func showFilePicker() {
	fd := dialog.NewFileOpen(func(reader fyne.URIReadCloser, err error) {
		if err != nil || reader == nil {
			return
		}
		path := reader.URI().Path()
		reader.Close()
		selectedFiles = nil
		collectVideos(path, &selectedFiles)
		updateFileListLabel()
	}, window)
	fd.Show()
}

func getTargetSize() (int, int, error) {
	switch resModeSel.Selected {
	case "1920x1080 (Full HD)":
		return 1920, 1080, nil
	case "1280x720 (HD)":
		return 1280, 720, nil
	case "960x540":
		return 960, 540, nil
	case "854x480":
		return 854, 480, nil
	case "640x360":
		return 640, 360, nil
	default:
		w, err1 := strconv.Atoi(strings.TrimSpace(customWEntry.Text))
		h, err2 := strconv.Atoi(strings.TrimSpace(customHEntry.Text))
		if err1 != nil || err2 != nil || w <= 0 || h <= 0 {
			return 0, 0, fmt.Errorf("укажи корректную ширину и высоту")
		}
		return w, h, nil
	}
}

func getCropMode() string {
	switch cropModeSel.Selected {
	case "Вписать с полями (letterbox)":
		return "letterbox"
	case "Растянуть":
		return "stretch"
	default:
		return "crop"
	}
}

func getMirrorMode() string {
	switch mirrorSel.Selected {
	case "Горизонтально":
		return "horizontal"
	case "Вертикально":
		return "vertical"
	default:
		return "none"
	}
}

func getFormat() string {
	switch formatSel.Selected {
	case "WebM (VP8 + Vorbis)":
		return "webm_vp8"
	case "OGV (Theora + Vorbis)":
		return "ogv"
	default:
		return "webm_vp9"
	}
}

func checkFFmpegPresent() bool {
	if _, err := os.Stat(ffmpegPath()); err != nil {
		return false
	}
	if _, err := os.Stat(ffprobePath()); err != nil {
		return false
	}
	return true
}

func getMode() string {
	switch modeSel.Selected {
	case "Только обрезка (без пересжатия, быстро, без потери качества)":
		return "trim_only"
	case "И обрезка, и сжатие/конвертация":
		return "both"
	default:
		return "compress_only"
	}
}

func processFiles() {
	if len(selectedFiles) == 0 {
		dialog.ShowInformation("Нет файлов", "Сначала выбери видео или папку с видео.", window)
		return
	}

	if !checkFFmpegPresent() {
		dialog.ShowInformation("ffmpeg не найден",
			"Файлы ffmpeg.exe и ffprobe.exe должны лежать в той же папке, что и эта программа.", window)
		return
	}

	mode := getMode()

	var targetW, targetH int
	var cropMode, mirrorMode, format string
	var crf int

	if mode != "trim_only" {
		var err error
		targetW, targetH, err = getTargetSize()
		if err != nil {
			dialog.ShowError(err, window)
			return
		}
		cropMode = getCropMode()
		mirrorMode = getMirrorMode()
		format = getFormat()
		crf = int(crfSlider.Value)
	}

	var trimStart, trimEnd float64
	if mode != "compress_only" {
		trimStart, _ = strconv.ParseFloat(strings.TrimSpace(trimStartEntry.Text), 64)
		trimEnd, _ = strconv.ParseFloat(strings.TrimSpace(trimEndEntry.Text), 64)
		if trimEnd <= trimStart {
			dialog.ShowError(fmt.Errorf("конец фрагмента должен быть больше начала"), window)
			return
		}
	}

	outDir := "output"
	os.MkdirAll(outDir, 0755)

	total := len(selectedFiles)
	var failed []string

	for i, path := range selectedFiles {
		name := filepath.Base(path)

		var outName string
		if mode == "trim_only" {
			// формат не меняется - оставляем исходное расширение
			outName = strings.TrimSuffix(name, filepath.Ext(name)) + "_cut" + filepath.Ext(name)
		} else {
			ext := outExtForFormat(format)
			outName = strings.TrimSuffix(name, filepath.Ext(name)) + ext
		}
		outPath := filepath.Join(outDir, outName)

		statusLabel.SetText(fmt.Sprintf("Обрабатываю файл %d/%d: %s — 0%%", i+1, total, name))
		progressBar.SetValue(float64(i) / float64(total))

		progressCb := func(frac float64) {
			overall := (float64(i) + frac) / float64(total)
			progressBar.SetValue(overall)
			statusLabel.SetText(fmt.Sprintf("Обрабатываю файл %d/%d: %s — %.0f%%", i+1, total, name, frac*100))
		}

		var err error
		switch mode {
		case "trim_only":
			err = runTrimOnly(path, outPath, trimStart, trimEnd, progressCb)
		case "compress_only":
			err = runFFmpeg(path, outPath, targetW, targetH, cropMode, mirrorMode, format, crf,
				false, 0, 0, progressCb)
		default: // both
			err = runFFmpeg(path, outPath, targetW, targetH, cropMode, mirrorMode, format, crf,
				true, trimStart, trimEnd, progressCb)
		}

		if err != nil {
			failed = append(failed, name)
		}
	}

	progressBar.SetValue(1)
	okCount := total - len(failed)
	statusLabel.SetText(fmt.Sprintf("Готово! Успешно: %d из %d.", okCount, total))

	msg := fmt.Sprintf("Обработано успешно: %d из %d.\nРезультат сохранён в папку \"output\" рядом с программой.", okCount, total)
	if len(failed) > 0 {
		msg += fmt.Sprintf("\n\nНе удалось обработать (%d):\n%s", len(failed), strings.Join(failed, "\n"))
	}
	dialog.ShowInformation("Готово", msg, window)
}

func main() {
	a := app.New()
	window = a.NewWindow("RenPy Video Tool")
	window.Resize(fyne.NewSize(560, 700))

	fileListLabel = widget.NewLabel("")
	fileListLabel.Wrapping = fyne.TextWrapWord
	updateFileListLabel()

	browseBtn := widget.NewButton("Выбрать видео или папку...", func() {
		showFilePicker()
	})

	modeSel = widget.NewRadioGroup([]string{
		"Только сжатие/конвертация (без обрезки)",
		"Только обрезка (без пересжатия, быстро, без потери качества)",
		"И обрезка, и сжатие/конвертация",
	}, func(s string) {
		if compressBox == nil || trimBox == nil {
			return
		}
		switch s {
		case "Только обрезка (без пересжатия, быстро, без потери качества)":
			compressBox.Hide()
			trimBox.Show()
		case "И обрезка, и сжатие/конвертация":
			compressBox.Show()
			trimBox.Show()
		default:
			compressBox.Show()
			trimBox.Hide()
		}
	})
	modeSel.SetSelected("Только сжатие/конвертация (без обрезки)")

	resModeSel = widget.NewRadioGroup([]string{
		"1920x1080 (Full HD)",
		"1280x720 (HD)",
		"960x540",
		"854x480",
		"640x360",
		"Своё разрешение",
	}, func(s string) {})
	resModeSel.SetSelected("1280x720 (HD)")

	customWEntry = widget.NewEntry()
	customWEntry.SetPlaceHolder("Ширина, px")
	customHEntry = widget.NewEntry()
	customHEntry.SetPlaceHolder("Высота, px")
	customRow := container.NewGridWithColumns(2, customWEntry, customHEntry)

	cropModeSel = widget.NewRadioGroup([]string{
		"Обрезать (crop)",
		"Вписать с полями (letterbox)",
		"Растянуть",
	}, func(s string) {})
	cropModeSel.SetSelected("Вписать с полями (letterbox)")

	formatSel = widget.NewRadioGroup([]string{
		"WebM (VP9 + Opus) — рекомендуется",
		"WebM (VP8 + Vorbis)",
		"OGV (Theora + Vorbis)",
	}, func(s string) {})
	formatSel.SetSelected("WebM (VP9 + Opus) — рекомендуется")

	mirrorSel = widget.NewRadioGroup([]string{
		"Без отзеркаливания",
		"Горизонтально",
		"Вертикально",
	}, func(s string) {})
	mirrorSel.SetSelected("Без отзеркаливания")

	crfLabel = widget.NewLabel("Качество (CRF): 32  (меньше = лучше качество, больше файл)")
	crfSlider = widget.NewSlider(15, 50)
	crfSlider.SetValue(32)
	crfSlider.OnChanged = func(v float64) {
		crfLabel.SetText(fmt.Sprintf("Качество (CRF): %.0f  (меньше = лучше качество, больше файл)", v))
	}

	trimStartEntry = widget.NewEntry()
	trimStartEntry.SetPlaceHolder("Начало, сек (например 5)")
	trimEndEntry = widget.NewEntry()
	trimEndEntry.SetPlaceHolder("Конец, сек (например 15)")
	trimRow := container.NewGridWithColumns(2, trimStartEntry, trimEndEntry)
	trimNote := widget.NewLabel(
		"Подсказка: в режиме «без пересжатия» точка обрезки округляется до ближайшего\n" +
			"опорного кадра (keyframe) — фрагмент может начаться на 1-2 сек раньше/позже\n" +
			"указанного времени, зато без потери качества. Если нужна точность до кадра —\n" +
			"выбери режим «И обрезка, и сжатие» (но тогда видео будет пересжато).")
	trimNote.Wrapping = fyne.TextWrapWord

	progressBar = widget.NewProgressBar()
	statusLabel = widget.NewLabel("Готов к работе")

	startBtn := widget.NewButton("Начать обработку", func() {
		go processFiles()
	})

	compressBox = container.NewVBox(
		widget.NewLabel("Целевое разрешение:"),
		resModeSel,
		customRow,
		widget.NewSeparator(),
		widget.NewLabel("Если пропорции не совпадают:"),
		cropModeSel,
		widget.NewSeparator(),
		widget.NewLabel("Формат вывода (для RenPy):"),
		formatSel,
		widget.NewSeparator(),
		widget.NewLabel("Отзеркаливание:"),
		mirrorSel,
		widget.NewSeparator(),
		crfLabel,
		crfSlider,
	)

	trimBox = container.NewVBox(
		widget.NewLabel("Обрезка по времени:"),
		trimRow,
		trimNote,
	)

	// начальное состояние видимости соответствует выбранному по умолчанию режиму
	trimBox.Hide()

	content := container.NewVBox(
		widget.NewLabelWithStyle("RenPy Video Tool", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		fileListLabel,
		browseBtn,
		widget.NewSeparator(),
		widget.NewLabel("Что нужно сделать:"),
		modeSel,
		widget.NewSeparator(),
		compressBox,
		trimBox,
		widget.NewSeparator(),
		startBtn,
		progressBar,
		statusLabel,
	)

	window.SetContent(container.NewVScroll(content))

	window.SetOnDropped(func(pos fyne.Position, items []fyne.URI) {
		selectedFiles = nil
		for _, item := range items {
			collectVideos(item.Path(), &selectedFiles)
		}
		updateFileListLabel()
	})

	window.ShowAndRun()
}
