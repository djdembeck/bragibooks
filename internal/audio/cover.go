package audio

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// ErrNoCover reports that a source carries no cover art. It is a normal
// outcome, not a failure: callers keep the UI's missing-cover placeholder.
var ErrNoCover = errors.New("source has no cover art")

// sidecarCoverNames are the conventional folder-art basenames, in priority
// order. A folder source that has one of these does not need embedded art.
var sidecarCoverNames = []string{"cover", "folder", "front", "album", "artwork"}

// imageExtensions are the sidecar image formats ffmpeg can decode.
var imageExtensions = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".webp": true, ".bmp": true,
}

// IsAudioFile reports whether name carries an audiobook audio extension.
func IsAudioFile(name string) bool {
	ext := strings.ToLower(name)
	for _, audioExt := range []string{".mp3", ".m4a", ".m4b", ".mp4", ".aac", ".ogg", ".flac", ".wma"} {
		if strings.HasSuffix(ext, audioExt) {
			return true
		}
	}
	return false
}

// ExtractCover writes the cover art that srcPath already carries to
// destPath as a JPEG.
//
// srcPath is either a bare media file or a folder of audio files. For a
// folder, a conventional sidecar image (cover.jpg, folder.jpg, …) wins;
// otherwise the first audio file's embedded picture stream is used.
//
// Art already embedded in the media is read with ffmpeg (a documented
// runtime dependency, present in the Docker image). ErrNoCover is returned
// when the source has neither a sidecar image nor an embedded picture
// stream, so callers can fall back to the empty-cover placeholder without
// logging a failure.
func ExtractCover(ctx context.Context, ffmpeg, srcPath, destPath string) error {
	if ffmpeg == "" {
		return errors.New("no ffmpeg binary configured")
	}
	if srcPath == "" {
		return errors.New("source path is empty")
	}

	input, err := coverSource(srcPath)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return fmt.Errorf("create covers directory: %w", err)
	}

	// Select the first picture stream (an attached_pic in an .m4b, an APIC
	// frame in an .mp3, or the sidecar image itself), take one frame, and
	// normalize it to a JPEG no wider than 1024px so a 3000px scan does not
	// become a multi-megabyte thumbnail payload.
	cmd := exec.CommandContext(
		ctx, ffmpeg,
		"-v", "error",
		"-nostdin",
		"-y",
		"-i", input,
		"-map", "0:v:0",
		"-frames:v", "1",
		"-vf", `scale=trunc(min(1024\,iw)/2)*2:-2`,
		"-c:v", "mjpeg",
		"-q:v", "3",
		"-f", "image2",
		destPath,
	)
	out, runErr := cmd.CombinedOutput()
	if runErr != nil {
		os.Remove(destPath)
		if errors.Is(runErr, exec.ErrNotFound) {
			return fmt.Errorf("ffmpeg not found: %w", runErr)
		}
		// ffmpeg exits non-zero both for undecodable media and for media
		// with no picture stream; both mean "this source has no usable
		// cover" for the caller.
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = runErr.Error()
		}
		return fmt.Errorf("%w: %s", ErrNoCover, msg)
	}

	info, err := os.Stat(destPath)
	if err != nil || info.Size() == 0 {
		os.Remove(destPath)
		return ErrNoCover
	}
	return nil
}

// coverSource resolves the file ExtractCover should read: the folder's
// sidecar image when present, otherwise the first audio file in the
// folder, or srcPath itself when it is already a file.
func coverSource(srcPath string) (string, error) {
	info, err := os.Stat(srcPath)
	if err != nil {
		return "", fmt.Errorf("stat source: %w", err)
	}
	if !info.IsDir() {
		return srcPath, nil
	}

	entries, err := os.ReadDir(srcPath)
	if err != nil {
		return "", fmt.Errorf("read source folder: %w", err)
	}

	var images, audioFiles []string
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		name := entry.Name()
		ext := strings.ToLower(filepath.Ext(name))
		switch {
		case imageExtensions[ext]:
			images = append(images, name)
		case IsAudioFile(name):
			audioFiles = append(audioFiles, name)
		}
	}

	if image := pickSidecar(images); image != "" {
		return filepath.Join(srcPath, image), nil
	}
	if len(audioFiles) > 0 {
		sort.Strings(audioFiles)
		return filepath.Join(srcPath, audioFiles[0]), nil
	}
	return "", fmt.Errorf("%w: no image or audio file in %s", ErrNoCover, srcPath)
}

// pickSidecar returns the highest-priority conventional cover image from
// images, falling back to the first image when none of the conventional
// basenames match. Returns "" when images is empty.
func pickSidecar(images []string) string {
	if len(images) == 0 {
		return ""
	}
	sort.Strings(images)
	for _, want := range sidecarCoverNames {
		for _, name := range images {
			if strings.EqualFold(strings.TrimSuffix(name, filepath.Ext(name)), want) {
				return name
			}
		}
	}
	return images[0]
}
