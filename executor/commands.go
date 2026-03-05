// SPDX-License-Identifier: MPL-2.0
// Copyright 2026 Sagacient <sagacient@gmail.com>
//
// See CONTRIBUTORS.md for full contributor list.

package executor

import (
	"fmt"
	"path/filepath"
	"strings"
)

// ExtractAudioCommand generates a shell script to extract audio from a video file.
func ExtractAudioCommand(inputContainerPath, outputFormat, startTime, endTime, bitrate string) string {
	outputFile := fmt.Sprintf("/output/audio.%s", outputFormat)

	var sb strings.Builder
	sb.WriteString("#!/bin/bash\nset -euo pipefail\n\n")
	sb.WriteString(fmt.Sprintf("echo 'Extracting audio from: %s'\n", filepath.Base(inputContainerPath)))
	sb.WriteString(fmt.Sprintf("echo 'Output format: %s'\n", outputFormat))
	sb.WriteString(fmt.Sprintf("echo 'Bitrate: %s'\n\n", bitrate))

	// Build ffmpeg command
	sb.WriteString("ffmpeg -y -hide_banner -loglevel warning")

	if startTime != "" {
		sb.WriteString(fmt.Sprintf(" -ss '%s'", startTime))
	}

	sb.WriteString(fmt.Sprintf(" -i '%s'", inputContainerPath))

	if endTime != "" {
		sb.WriteString(fmt.Sprintf(" -to '%s'", endTime))
	}

	sb.WriteString(fmt.Sprintf(" -vn -b:a '%s' '%s'\n\n", bitrate, outputFile))

	// Report output
	sb.WriteString("if [ -f '" + outputFile + "' ]; then\n")
	sb.WriteString("  SIZE=$(stat -c%s '" + outputFile + "' 2>/dev/null || stat -f%z '" + outputFile + "' 2>/dev/null || echo 'unknown')\n")
	sb.WriteString("  DURATION=$(ffprobe -v quiet -show_entries format=duration -of csv=p=0 '" + outputFile + "' 2>/dev/null || echo 'unknown')\n")
	sb.WriteString("  echo ''\n")
	sb.WriteString("  echo 'Audio extraction complete:'\n")
	sb.WriteString("  echo \"  File: " + fmt.Sprintf("audio.%s", outputFormat) + "\"\n")
	sb.WriteString("  echo \"  Size: ${SIZE} bytes\"\n")
	sb.WriteString("  echo \"  Duration: ${DURATION}s\"\n")
	sb.WriteString("else\n")
	sb.WriteString("  echo 'Error: Output file was not created' >&2\n")
	sb.WriteString("  exit 1\n")
	sb.WriteString("fi\n")

	return sb.String()
}

// ExtractFramesCommand generates a shell script to extract sampled frames from a video.
func ExtractFramesCommand(inputContainerPath, mode, param, outputFormat string, quality int) string {
	var sb strings.Builder
	sb.WriteString("#!/bin/bash\nset -euo pipefail\n\n")
	sb.WriteString("OUTPUT_DIR='/output'\n")
	sb.WriteString(fmt.Sprintf("echo 'Extracting frames from: %s'\n", filepath.Base(inputContainerPath)))
	sb.WriteString(fmt.Sprintf("echo 'Mode: %s'\n", mode))
	sb.WriteString(fmt.Sprintf("echo 'Format: %s'\n\n", outputFormat))

	// Get video info
	sb.WriteString(fmt.Sprintf("DURATION=$(ffprobe -v quiet -show_entries format=duration -of csv=p=0 '%s' 2>/dev/null || echo '0')\n", inputContainerPath))
	sb.WriteString(fmt.Sprintf("FPS=$(ffprobe -v quiet -select_streams v:0 -show_entries stream=r_frame_rate -of csv=p=0 '%s' 2>/dev/null || echo '30/1')\n\n", inputContainerPath))

	// Quality args for JPEG
	qualityArg := ""
	if outputFormat == "jpg" || outputFormat == "jpeg" {
		// FFmpeg qscale:v for JPEG is 2-31, where 2 is best
		qscale := (100-quality)*29/100 + 2
		qualityArg = fmt.Sprintf("-qscale:v %d", qscale)
	}

	switch mode {
	case "interval":
		sb.WriteString(fmt.Sprintf("echo 'Sampling interval: every %ss'\n", param))
		sb.WriteString(fmt.Sprintf("ffmpeg -y -hide_banner -loglevel warning -i '%s' -vf 'fps=1/%s' %s \"${OUTPUT_DIR}/frame_%%05d.%s\"\n",
			inputContainerPath, param, qualityArg, outputFormat))
	case "count":
		sb.WriteString(fmt.Sprintf("echo 'Extracting %s evenly-spaced frames'\n", param))
		sb.WriteString("TOTAL_FRAMES=$(echo \"$DURATION * $(echo $FPS | bc -l)\" | bc -l 2>/dev/null | cut -d. -f1 || echo '300')\n")
		sb.WriteString(fmt.Sprintf("INTERVAL=$((TOTAL_FRAMES / %s))\n", param))
		sb.WriteString("[ \"$INTERVAL\" -lt 1 ] && INTERVAL=1\n")
		sb.WriteString(fmt.Sprintf("ffmpeg -y -hide_banner -loglevel warning -i '%s' -vf \"select='not(mod(n\\,$INTERVAL))',setpts=N/TB\" -frames:v %s %s \"${OUTPUT_DIR}/frame_%%05d.%s\"\n",
			inputContainerPath, param, qualityArg, outputFormat))
	case "keyframes":
		sb.WriteString("echo 'Extracting keyframes (I-frames) only'\n")
		sb.WriteString(fmt.Sprintf("ffmpeg -y -hide_banner -loglevel warning -i '%s' -vf \"select='eq(pict_type\\,I)'\" -vsync vfr %s \"${OUTPUT_DIR}/frame_%%05d.%s\"\n",
			inputContainerPath, qualityArg, outputFormat))
	}

	// Report results
	sb.WriteString("\n")
	sb.WriteString(fmt.Sprintf("FRAME_COUNT=$(find \"${OUTPUT_DIR}\" -name 'frame_*.%s' -type f | wc -l)\n", outputFormat))
	sb.WriteString("echo ''\n")
	sb.WriteString("echo 'Frame extraction complete:'\n")
	sb.WriteString("echo \"  Frames extracted: ${FRAME_COUNT}\"\n")
	sb.WriteString(fmt.Sprintf("echo '  Format: %s'\n", outputFormat))
	sb.WriteString("\nfor f in \"${OUTPUT_DIR}\"/frame_*." + outputFormat + "; do\n")
	sb.WriteString("  [ -f \"$f\" ] || continue\n")
	sb.WriteString("  SIZE=$(stat -c%s \"$f\" 2>/dev/null || stat -f%z \"$f\" 2>/dev/null || echo 'unknown')\n")
	sb.WriteString("  echo \"  - $(basename \"$f\") (${SIZE} bytes)\"\n")
	sb.WriteString("done\n")

	return sb.String()
}

// ImagesToVideoCommand generates a shell script to create a video from images.
func ImagesToVideoCommand(imageContainerPaths []string, outputFormat string, fps int, durationPerImage float64, resolution, subtitleContainerPath string) string {
	outputFile := fmt.Sprintf("/output/video.%s", outputFormat)

	var sb strings.Builder
	sb.WriteString("#!/bin/bash\nset -euo pipefail\n\n")
	sb.WriteString(fmt.Sprintf("echo 'Creating video from %d images'\n", len(imageContainerPaths)))
	sb.WriteString(fmt.Sprintf("echo 'FPS: %d'\n", fps))
	sb.WriteString(fmt.Sprintf("echo 'Duration per image: %.1fs'\n", durationPerImage))
	sb.WriteString(fmt.Sprintf("echo 'Resolution: %s'\n\n", resolution))

	// Create concat demuxer file
	sb.WriteString("CONCAT_FILE='/tmp/concat_list.txt'\n")
	sb.WriteString("> \"$CONCAT_FILE\"\n\n")

	for _, imgPath := range imageContainerPaths {
		sb.WriteString(fmt.Sprintf("echo \"file '%s'\" >> \"$CONCAT_FILE\"\n", imgPath))
		sb.WriteString(fmt.Sprintf("echo \"duration %.1f\" >> \"$CONCAT_FILE\"\n", durationPerImage))
	}
	// FFmpeg concat requires last image repeated
	if len(imageContainerPaths) > 0 {
		sb.WriteString(fmt.Sprintf("echo \"file '%s'\" >> \"$CONCAT_FILE\"\n\n", imageContainerPaths[len(imageContainerPaths)-1]))
	}

	// Parse resolution
	parts := strings.Split(resolution, "x")
	width := "1920"
	height := "1080"
	if len(parts) == 2 {
		width = parts[0]
		height = parts[1]
	}

	// Build video filter
	vf := fmt.Sprintf("scale=%s:%s:force_original_aspect_ratio=decrease,pad=%s:%s:(ow-iw)/2:(oh-ih)/2:black", width, height, width, height)

	if subtitleContainerPath != "" {
		vf += fmt.Sprintf(",subtitles='%s'", subtitleContainerPath)
	}

	sb.WriteString(fmt.Sprintf("ffmpeg -y -hide_banner -loglevel warning -f concat -safe 0 -i \"$CONCAT_FILE\" -vf \"%s\" -r %d -c:v libx264 -pix_fmt yuv420p -preset medium -crf 23 '%s'\n\n",
		vf, fps, outputFile))

	sb.WriteString("rm -f \"$CONCAT_FILE\"\n\n")

	// Report
	sb.WriteString("if [ -f '" + outputFile + "' ]; then\n")
	sb.WriteString("  SIZE=$(stat -c%s '" + outputFile + "' 2>/dev/null || stat -f%z '" + outputFile + "' 2>/dev/null || echo 'unknown')\n")
	sb.WriteString("  DURATION=$(ffprobe -v quiet -show_entries format=duration -of csv=p=0 '" + outputFile + "' 2>/dev/null || echo 'unknown')\n")
	sb.WriteString("  echo ''\n")
	sb.WriteString("  echo 'Video creation complete:'\n")
	sb.WriteString("  echo \"  File: " + fmt.Sprintf("video.%s", outputFormat) + "\"\n")
	sb.WriteString("  echo \"  Size: ${SIZE} bytes\"\n")
	sb.WriteString("  echo \"  Duration: ${DURATION}s\"\n")
	sb.WriteString(fmt.Sprintf("  echo '  Images used: %d'\n", len(imageContainerPaths)))
	sb.WriteString("else\n")
	sb.WriteString("  echo 'Error: Output file was not created' >&2\n")
	sb.WriteString("  exit 1\n")
	sb.WriteString("fi\n")

	return sb.String()
}

// PPTSlidesToImagesCommand generates a shell script to export PPT slides as images.
func PPTSlidesToImagesCommand(inputContainerPath, outputFormat, slides string) string {
	var sb strings.Builder
	sb.WriteString("#!/bin/bash\nset -euo pipefail\n\n")
	sb.WriteString(fmt.Sprintf("echo 'Exporting slides from: %s'\n", filepath.Base(inputContainerPath)))
	sb.WriteString(fmt.Sprintf("echo 'Output format: %s'\n\n", outputFormat))

	// Convert PPTX to PDF using LibreOffice headless
	sb.WriteString("TEMP_DIR=$(mktemp -d)\n")
	sb.WriteString("echo 'Converting presentation to PDF...'\n")
	sb.WriteString(fmt.Sprintf("libreoffice --headless --convert-to pdf --outdir \"$TEMP_DIR\" '%s' 2>/dev/null\n\n", inputContainerPath))

	sb.WriteString("PDF_FILE=$(find \"$TEMP_DIR\" -name '*.pdf' -type f | head -1)\n")
	sb.WriteString("if [ ! -f \"$PDF_FILE\" ]; then\n")
	sb.WriteString("  echo 'Error: LibreOffice conversion failed' >&2\n")
	sb.WriteString("  rm -rf \"$TEMP_DIR\"\n")
	sb.WriteString("  exit 1\n")
	sb.WriteString("fi\n\n")

	// Convert PDF pages to images
	sb.WriteString("echo 'Converting slides to images...'\n")
	if outputFormat == "png" {
		sb.WriteString("pdftoppm -png -r 300 \"$PDF_FILE\" \"$TEMP_DIR/slide\" 2>/dev/null || ")
		sb.WriteString("ffmpeg -y -hide_banner -loglevel warning -i \"$PDF_FILE\" \"$TEMP_DIR/slide-%03d.png\"\n\n")
	} else {
		sb.WriteString("pdftoppm -jpeg -jpegopt quality=95 -r 300 \"$PDF_FILE\" \"$TEMP_DIR/slide\" 2>/dev/null || ")
		sb.WriteString(fmt.Sprintf("ffmpeg -y -hide_banner -loglevel warning -i \"$PDF_FILE\" \"$TEMP_DIR/slide-%%03d.%s\"\n\n", outputFormat))
	}

	// Move/filter slides to output
	sb.WriteString("SLIDE_NUM=0\n")
	sb.WriteString("EXPORTED=0\n\n")

	sb.WriteString(fmt.Sprintf("for img in \"$TEMP_DIR\"/slide*.%s \"$TEMP_DIR\"/slide-*.%s; do\n", outputFormat, outputFormat))
	sb.WriteString("  [ ! -f \"$img\" ] && continue\n")
	sb.WriteString("  SLIDE_NUM=$((SLIDE_NUM + 1))\n")

	if slides != "" {
		// Filter by specific slide numbers
		sb.WriteString(fmt.Sprintf("  if ! echo ',%s,' | grep -q \",$SLIDE_NUM,\"; then\n", slides))
		sb.WriteString("    continue\n")
		sb.WriteString("  fi\n")
	}

	sb.WriteString(fmt.Sprintf("  cp \"$img\" \"/output/slide_$(printf '%%03d' $SLIDE_NUM).%s\"\n", outputFormat))
	sb.WriteString("  EXPORTED=$((EXPORTED + 1))\n")
	sb.WriteString("done\n\n")

	sb.WriteString("rm -rf \"$TEMP_DIR\"\n\n")

	// Report
	sb.WriteString("echo ''\n")
	sb.WriteString("echo 'Slide export complete:'\n")
	sb.WriteString("echo \"  Total slides: ${SLIDE_NUM}\"\n")
	sb.WriteString("echo \"  Exported: ${EXPORTED}\"\n")
	sb.WriteString(fmt.Sprintf("echo '  Format: %s'\n\n", outputFormat))

	sb.WriteString(fmt.Sprintf("for f in /output/slide_*.%s; do\n", outputFormat))
	sb.WriteString("  [ -f \"$f\" ] || continue\n")
	sb.WriteString("  SIZE=$(stat -c%s \"$f\" 2>/dev/null || stat -f%z \"$f\" 2>/dev/null || echo 'unknown')\n")
	sb.WriteString("  echo \"  - $(basename \"$f\") (${SIZE} bytes)\"\n")
	sb.WriteString("done\n")

	return sb.String()
}

// PPTExtractAudioCommand generates a shell script to extract media from PPTX and convert to audio.
func PPTExtractAudioCommand(inputContainerPath, outputFormat, bitrate string) string {
	var sb strings.Builder
	sb.WriteString("#!/bin/bash\nset -euo pipefail\n\n")
	sb.WriteString(fmt.Sprintf("echo 'Extracting media from: %s'\n", filepath.Base(inputContainerPath)))
	sb.WriteString(fmt.Sprintf("echo 'Output format: %s'\n", outputFormat))
	sb.WriteString(fmt.Sprintf("echo 'Bitrate: %s'\n\n", bitrate))

	// Use the Python script baked into the Docker image
	sb.WriteString(fmt.Sprintf("python3 /scripts/ppt_extract_media.py '%s' /output '%s' '%s'\n",
		inputContainerPath, outputFormat, bitrate))

	return sb.String()
}
