package audio

import (
	"context"
	"errors"
	"image"
	_ "image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// stubFFmpeg writes an executable shell script standing in for ffmpeg. The
// body receives the real argv; `for last; do :; done` yields the output path.
func stubFFmpeg(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ffmpeg")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatalf("write stub ffmpeg: %v", err)
	}
	return path
}

func TestExtractCoverPrefersFolderSidecar(t *testing.T) {
	srcDir := t.TempDir()
	write := func(name string) string {
		path := filepath.Join(srcDir, name)
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		return path
	}
	write("zzz.m4b")
	sidecar := write("cover.JPG")

	argsFile := filepath.Join(t.TempDir(), "args")
	ffmpeg := stubFFmpeg(t, `echo "$@" > `+argsFile+`
for last; do :; done
printf 'jpeg-bytes' > "$last"`)

	dest := filepath.Join(t.TempDir(), "1.jpg")
	if err := ExtractCover(context.Background(), ffmpeg, srcDir, dest); err != nil {
		t.Fatalf("ExtractCover: %v", err)
	}

	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	if !strings.Contains(string(args), sidecar) {
		t.Errorf("ffmpeg input does not use sidecar %q; args: %s", sidecar, args)
	}
	if _, err := os.Stat(dest); err != nil {
		t.Errorf("dest not written: %v", err)
	}
}

func TestExtractCoverUsesFirstAudioFileInFolder(t *testing.T) {
	srcDir := t.TempDir()
	for _, name := range []string{"b.m4b", "a.mp3", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(srcDir, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	argsFile := filepath.Join(t.TempDir(), "args")
	ffmpeg := stubFFmpeg(t, `echo "$@" > `+argsFile+`
for last; do :; done
printf 'jpeg-bytes' > "$last"`)

	if err := ExtractCover(context.Background(), ffmpeg, srcDir, filepath.Join(t.TempDir(), "1.jpg")); err != nil {
		t.Fatalf("ExtractCover: %v", err)
	}

	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	if !strings.Contains(string(args), filepath.Join(srcDir, "a.mp3")) {
		t.Errorf("ffmpeg input is not the first audio file; args: %s", args)
	}
}

func TestExtractCoverFolderWithoutArtIsNoCover(t *testing.T) {
	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	ffmpeg := stubFFmpeg(t, `echo "should not run" >&2; exit 1`)
	err := ExtractCover(context.Background(), ffmpeg, srcDir, filepath.Join(t.TempDir(), "1.jpg"))
	if !errors.Is(err, ErrNoCover) {
		t.Fatalf("err = %v; want ErrNoCover", err)
	}
}

func TestExtractCoverFFmpegFailureIsNoCover(t *testing.T) {
	src := filepath.Join(t.TempDir(), "book.m4b")
	if err := os.WriteFile(src, []byte("not really audio"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	dest := filepath.Join(t.TempDir(), "1.jpg")
	ffmpeg := stubFFmpeg(t, `echo "Stream map '0:v:0' matches no streams." >&2; exit 1`)

	err := ExtractCover(context.Background(), ffmpeg, src, dest)
	if !errors.Is(err, ErrNoCover) {
		t.Fatalf("err = %v; want ErrNoCover", err)
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Errorf("partial output left behind: %v", statErr)
	}
}

func TestExtractCoverMissingSource(t *testing.T) {
	ffmpeg := stubFFmpeg(t, `for last; do :; done; printf x > "$last"`)
	err := ExtractCover(context.Background(), ffmpeg, filepath.Join(t.TempDir(), "absent.m4b"), filepath.Join(t.TempDir(), "1.jpg"))
	if err == nil || errors.Is(err, ErrNoCover) {
		t.Fatalf("err = %v; want a source error, not ErrNoCover", err)
	}
}

// TestExtractCoverRealMedia runs the real ffmpeg end to end: it builds an
// .m4b with an oversized embedded picture stream and checks the extracted
// JPEG is downscaled to the documented 1024px cap.
func TestExtractCoverRealMedia(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not on PATH")
	}
	dir := t.TempDir()
	cover := filepath.Join(dir, "cover.png")
	media := filepath.Join(dir, "book.m4b")

	run := func(args ...string) {
		t.Helper()
		if out, err := exec.Command(ffmpeg, args...).CombinedOutput(); err != nil {
			t.Fatalf("ffmpeg %v: %v — %s", args, err, out)
		}
	}
	run("-v", "error", "-y", "-f", "lavfi", "-i", "color=c=red:s=2400x1800:d=1", "-frames:v", "1", cover)
	run("-v", "error", "-y",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=1",
		"-i", cover,
		"-map", "0:a", "-map", "1:v",
		"-c:a", "aac", "-c:v", "mjpeg",
		"-disposition:v", "attached_pic",
		"-f", "mp4", media,
	)

	dest := filepath.Join(dir, "1.jpg")
	if err := ExtractCover(context.Background(), ffmpeg, media, dest); err != nil {
		t.Fatalf("ExtractCover: %v", err)
	}

	f, err := os.Open(dest)
	if err != nil {
		t.Fatalf("open output: %v", err)
	}
	defer f.Close()
	cfg, format, err := image.DecodeConfig(f)
	if err != nil {
		t.Fatalf("output is not a decodable image: %v", err)
	}
	if format != "jpeg" {
		t.Errorf("output format = %q; want jpeg", format)
	}
	if cfg.Width > 1024 {
		t.Errorf("output width = %d; want <= 1024", cfg.Width)
	}
	if cfg.Width != 1024 || cfg.Height != 768 {
		t.Errorf("output = %dx%d; want 1024x768 preserving 4:3", cfg.Width, cfg.Height)
	}
}
