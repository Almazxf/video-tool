package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

type fragmentRow struct {
	startEntry *widget.Entry
	endEntry   *widget.Entry
	row        *fyne.Container
}

var (
	window fyne.Window

	modeSel     *widget.RadioGroup
	compressBox *fyne.Container
	trimBox     *fyne.Container

	resModeSel   *widget.RadioGroup
	customWEntry *widget.Entry
	customHEntry *widget.Entry
	cropModeSel  *widget.RadioGroup
	formatSel    *widget.RadioGroup
	mirrorSel    *widget.RadioGroup
	crfSlider    *widget.Slider
	crfLabel     *widget.Label

	fragmentsListBox *fyne.Container
	fragments        []*fragmentRow

	previewSecEntry *widget.Entry
	previewImage    *canvas.Image
	previewStatus   *widget.Label

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

func checkFFmpegPresent() bool {
	if _, err := os.Stat(ffmpegPath()); err != nil {
		return false
	}
	if _, err := os.Stat(ffprobePath()); err != nil {
		return false
	}
	return true
}

// ----- управление списком фрагментов -----

func rebuildFragmentsListBox() {
	objs := make([]fyne.CanvasObject, 0, len(fragments))
	for _, f := range fragments {
		objs = append(objs, f.row)
	}
	fragmentsListBox.Objects = objs
	fragmentsListBox.Refresh()
}

func addFragmentRow() {
	startEntry := widget.NewEntry()
	startEntry.SetPlaceHolder("Начало, сек")
	endEntry := widget.NewEntry()
	endEntry.SetPlaceHolder("Конец, сек")

	fr := &fragmentRow{startEntry: startEntry, endEntry: endEntry}

	removeBtn := widget.NewButton("Удалить", func() {
		removeFragmentRow(fr)
	})

	fr.row = container.NewBorder(nil, nil, nil, removeBtn,
		container.NewGridWithColumns(2, startEntry, endEntry))

	fragments = append(fragments, fr)
	rebuildFragmentsListBox()
}

func removeFragmentRow(target *fragmentRow) {
	if len(fragments) <= 1 {
		return // хотя бы один фрагмент должен остаться
	}
	newList := make([]*fragmentRow, 0, len(fragments)-1)
	for _, f := range fragments {
		if f != target {
			newList = append(newList, f)
		}
	}
	fragments = newList
	rebuildFragmentsListBox()
}

// ----- превью -----

func doPreviewFrame() {
	if len(selectedFiles) == 0 {
		dialog.ShowInformation("Нет файла", "Сначала выбери видео.", window)
		return
	}
	if !checkFFmpegPresent() {
		dialog.ShowInformation("ffmpeg не найден", "ffmpeg.exe должен лежать рядом с программой.", window)
		return
	}
	sec, err := strconv.ParseFloat(strings.TrimSpace(previewSecEntry.Text), 64)
	if err != nil {
		dialog.ShowError(fmt.Errorf("укажи секунду числом, например 12.5"), window)
		return
	}

	previewStatus.SetText("Извлекаю кадр...")
	go func() {
		framePath := filepath.Join(os.TempDir(), "renpyvideotool_preview.jpg")
		err := extractFrame(selectedFiles[0], sec, framePath)
		if err != nil {
			previewStatus.SetText("Не удалось извлечь кадр на этой секунде (возможно видео короче).")
			return
		}
		previewImage.File = framePath
		previewImage.Refresh()
		previewStatus.SetText(fmt.Sprintf("Кадр на %.2f сек (файл: %s)", sec, filepath.Base(selectedFiles[0])))
	}()
}

func doOpenExternalPlayer() {
	if len(selectedFiles) == 0 {
		dialog.ShowInformation("Нет файла", "Сначала выбери видео.", window)
		return
	}
	if err := openInDefaultPlayer(selectedFiles[0]); err != nil {
		dialog.ShowError(err, window)
	}
}

// ----- обработка -----

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

	type frag struct{ start, end float64 }
	var fragList []frag

	if mode != "compress_only" {
		for idx, f := range fragments {
			s, errS := strconv.ParseFloat(strings.TrimSpace(f.startEntry.Text), 64)
			e, errE := strconv.ParseFloat(strings.TrimSpace(f.endEntry.Text), 64)
			if errS != nil || errE != nil || e <= s {
				dialog.ShowError(fmt.Errorf("проверь фрагмент №%d: начало/конец указаны неверно", idx+1), window)
				return
			}
			fragList = append(fragList, frag{s, e})
		}
		if len(fragList) == 0 {
			dialog.ShowError(fmt.Errorf("добавь хотя бы один фрагмент для обрезки"), window)
			return
		}
	} else {
		fragList = []frag{{0, 0}} // заглушка - не используется в compress_only
	}

	outDir := "output"
	os.MkdirAll(outDir, 0755)

	totalFiles := len(selectedFiles)
	totalSteps := totalFiles * len(fragList)
	step := 0
	var failed []string

	for _, path := range selectedFiles {
		name := filepath.Base(path)
		baseName := strings.TrimSuffix(name, filepath.Ext(name))

		for fi, fr := range fragList {
			var outName string
			multiFrag := mode != "compress_only" && len(fragList) > 1

			switch {
			case mode == "trim_only" && multiFrag:
				outName = fmt.Sprintf("%s_part%d%s", baseName, fi+1, filepath.Ext(name))
			case mode == "trim_only":
				outName = baseName + "_cut" + filepath.Ext(name)
			case mode == "compress_only":
				outName = baseName + outExtForFormat(format)
			case multiFrag:
				outName = fmt.Sprintf("%s_part%d%s", baseName, fi+1, outExtForFormat(format))
			default:
				outName = baseName + "_cut" + outExtForFormat(format)
			}

			outPath := filepath.Join(outDir, outName)

			label := outName
			statusLabel.SetText(fmt.Sprintf("Обрабатываю %d/%d: %s — 0%%", step+1, totalSteps, label))
			progressBar.SetValue(float64(step) / float64(totalSteps))

			progressCb := func(frac float64) {
				overall := (float64(step) + frac) / float64(totalSteps)
				progressBar.SetValue(overall)
				statusLabel.SetText(fmt.Sprintf("Обрабатываю %d/%d: %s — %.0f%%", step+1, totalSteps, label, frac*100))
			}

			var err error
			switch mode {
			case "trim_only":
				err = runTrimOnly(path, outPath, fr.start, fr.end, progressCb)
			case "compress_only":
				err = runFFmpeg(path, outPath, targetW, targetH, cropMode, mirrorMode, format, crf,
					false, 0, 0, progressCb)
			default: // both
				err = runFFmpeg(path, outPath, targetW, targetH, cropMode, mirrorMode, format, crf,
					true, fr.start, fr.end, progressCb)
			}

			if err != nil {
				failed = append(failed, label)
			}

			step++

			if mode == "compress_only" {
				break // для compress_only фрагменты не имеют смысла, обрабатываем файл один раз
			}
		}
	}

	progressBar.SetValue(1)
	okCount := totalSteps - len(failed)
	statusLabel.SetText(fmt.Sprintf("Готово! Успешно: %d из %d.", okCount, totalSteps))

	msg := fmt.Sprintf("Обработано успешно: %d из %d.\nРезультат сохранён в папку \"output\" рядом с программой.", okCount, totalSteps)
	if len(failed) > 0 {
		msg += fmt.Sprintf("\n\nНе удалось обработать (%d):\n%s", len(failed), strings.Join(failed, "\n"))
	}
	dialog.ShowInformation("Готово", msg, window)
}

func main() {
	a := app.New()
	window = a.NewWindow("RenPy Video Tool")
	window.Resize(fyne.NewSize(600, 760))

	fileListLabel = widget.NewLabel("")
	fileListLabel.Wrapping = fyne.TextWrapWord
	updateFileListLabel()

	browseBtn := widget.NewButton("Выбрать видео или папку...", func() {
		showFilePicker()
	})

	// --- превью ---
	previewSecEntry = widget.NewEntry()
	previewSecEntry.SetPlaceHolder("Секунда, например 12.5")
	previewBtn := widget.NewButton("Показать кадр", func() {
		doPreviewFrame()
	})
	externalBtn := widget.NewButton("Открыть в плеере по умолчанию", func() {
		doOpenExternalPlayer()
	})
	previewImage = canvas.NewImageFromFile("")
	previewImage.FillMode = canvas.ImageFillContain
	previewImage.SetMinSize(fyne.NewSize(400, 225))
	previewStatus = widget.NewLabel("Превью первого выбранного файла появится здесь.")
	previewStatus.Wrapping = fyne.TextWrapWord

	previewRow := container.NewBorder(nil, nil, nil, previewBtn, previewSecEntry)
	previewBox := container.NewVBox(
		widget.NewLabel("Превью кадра (по первому выбранному файлу):"),
		externalBtn,
		previewRow,
		previewImage,
		previewStatus,
	)

	// --- режим работы ---
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

	// --- настройки сжатия ---
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

	// --- список фрагментов ---
	fragmentsListBox = container.NewVBox()
	addFragmentRow() // одна строка по умолчанию

	addFragmentBtn := widget.NewButton("+ Добавить фрагмент", func() {
		addFragmentRow()
	})

	trimNote := widget.NewLabel(
		"Подсказка: в режиме «без пересжатия» точка обрезки округляется до ближайшего\n" +
			"опорного кадра (keyframe) — фрагмент может начаться на 1-2 сек раньше/позже\n" +
			"указанного времени, зато без потери качества. Если нужна точность до кадра —\n" +
			"выбери режим «И обрезка, и сжатие» (но тогда видео будет пересжато).\n\n" +
			"Если фрагментов несколько — для каждого видео получится несколько файлов:\n" +
			"video_part1, video_part2 и так далее.")
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
		widget.NewLabel("Фрагменты для обрезки:"),
		fragmentsListBox,
		addFragmentBtn,
		trimNote,
	)
	trimBox.Hide()

	content := container.NewVBox(
		widget.NewLabelWithStyle("RenPy Video Tool", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		fileListLabel,
		browseBtn,
		widget.NewSeparator(),
		previewBox,
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

