package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

func exeDir() string {
	exePath, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exePath)
}

func ffmpegPath() string {
	return filepath.Join(exeDir(), "ffmpeg.exe")
}

func ffprobePath() string {
	return filepath.Join(exeDir(), "ffprobe.exe")
}

// getDuration возвращает длительность видео в секундах через ffprobe
func getDuration(inputPath string) (float64, error) {
	cmd := exec.Command(ffprobePath(),
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		inputPath)
	out, err := cmd.Output()
	if err != nil {
		return 0, err
	}
	s := strings.TrimSpace(string(out))
	d, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, err
	}
	return d, nil
}

// buildFilterChain собирает -vf цепочку: ресайз/обрезка/letterbox + отзеркаливание
func buildFilterChain(targetW, targetH int, mode string, mirror string) string {
	var vf string
	switch mode {
	case "letterbox":
		vf = fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2", targetW, targetH, targetW, targetH)
	case "crop":
		vf = fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=increase,crop=%d:%d", targetW, targetH, targetW, targetH)
	default: // stretch
		vf = fmt.Sprintf("scale=%d:%d", targetW, targetH)
	}
	switch mirror {
	case "horizontal":
		vf += ",hflip"
	case "vertical":
		vf += ",vflip"
	}
	return vf
}

// buildCodecArgs возвращает аргументы кодека под выбранный формат
func buildCodecArgs(format string, crf int) []string {
	switch format {
	case "webm_vp8":
		return []string{
			"-c:v", "libvpx", "-crf", strconv.Itoa(crf), "-b:v", "1M",
			"-c:a", "libvorbis", "-b:a", "128k",
		}
	case "ogv":
		// для theora диапазон качества 0-10, переводим crf(0-63) грубо в q(0-10)
		q := 10 - (crf * 10 / 63)
		if q < 0 {
			q = 0
		}
		if q > 10 {
			q = 10
		}
		return []string{
			"-c:v", "libtheora", "-q:v", strconv.Itoa(q),
			"-c:a", "libvorbis", "-b:a", "128k",
		}
	default: // webm_vp9
		return []string{
			"-c:v", "libvpx-vp9", "-crf", strconv.Itoa(crf), "-b:v", "0",
			"-pix_fmt", "yuv420p",
			"-c:a", "libopus", "-b:a", "128k",
		}
	}
}

func outExtForFormat(format string) string {
	if format == "ogv" {
		return ".ogv"
	}
	return ".webm"
}

// runFFmpeg запускает обработку одного файла, вызывая onProgress(fraction) по ходу
func runFFmpeg(inputPath, outputPath string, targetW, targetH int, cropMode, mirror, format string, crf int,
	trimEnabled bool, trimStart, trimEnd float64, onProgress func(float64)) error {

	duration, _ := getDuration(inputPath)
	if trimEnabled && trimEnd > trimStart {
		duration = trimEnd - trimStart
	}

	args := []string{"-y"}
	if trimEnabled {
		args = append(args, "-ss", fmt.Sprintf("%.3f", trimStart))
	}
	args = append(args, "-i", inputPath)
	if trimEnabled {
		args = append(args, "-t", fmt.Sprintf("%.3f", trimEnd-trimStart))
	}

	vf := buildFilterChain(targetW, targetH, cropMode, mirror)
	args = append(args, "-vf", vf)
	args = append(args, buildCodecArgs(format, crf)...)
	args = append(args, "-progress", "pipe:1", outputPath)

	cmd := exec.Command(ffmpegPath(), args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = nil

	if err := cmd.Start(); err != nil {
		return err
	}

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "out_time_ms=") {
			valStr := strings.TrimPrefix(line, "out_time_ms=")
			val, err := strconv.ParseInt(valStr, 10, 64)
			if err == nil && duration > 0 {
				doneSec := float64(val) / 1000000.0
				frac := doneSec / duration
				if frac > 1 {
					frac = 1
				}
				if frac < 0 {
					frac = 0
				}
				onProgress(frac)
			}
		}
	}

	return cmd.Wait()
}

// runTrimOnly обрезает видео без пересжатия (-c copy), точка обрезки округляется до keyframe
func runTrimOnly(inputPath, outputPath string, trimStart, trimEnd float64, onProgress func(float64)) error {
	duration := trimEnd - trimStart

	args := []string{
		"-y",
		"-ss", fmt.Sprintf("%.3f", trimStart),
		"-i", inputPath,
		"-t", fmt.Sprintf("%.3f", duration),
		"-c", "copy",
		"-progress", "pipe:1",
		outputPath,
	}

	cmd := exec.Command(ffmpegPath(), args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = nil

	if err := cmd.Start(); err != nil {
		return err
	}

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "out_time_ms=") {
			valStr := strings.TrimPrefix(line, "out_time_ms=")
			val, err := strconv.ParseInt(valStr, 10, 64)
			if err == nil && duration > 0 {
				doneSec := float64(val) / 1000000.0
				frac := doneSec / duration
				if frac > 1 {
					frac = 1
				}
				if frac < 0 {
					frac = 0
				}
				onProgress(frac)
			}
		}
	}

	return cmd.Wait()
}
