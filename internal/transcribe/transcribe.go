package transcribe

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Transcribe runs Whisper CLI on the given audio file and returns the transcribed text.
// model: base | small | medium (tradeoff speed vs accuracy)
func Transcribe(audioPath, model string) (string, error) {
	if model == "" {
		model = "small"
	}

	dir := filepath.Dir(audioPath)

	cmd := exec.Command("whisper", audioPath,
		"--model", model,
		"--language", "id",
		"--output_format", "txt",
		"--output_dir", dir,
		"--fp16", "False", // hindari error di CPU-only
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("whisper gagal: %w\n%s", err, string(out))
	}

	// Whisper membuat file <nama_tanpa_ext>.txt di output_dir
	base := strings.TrimSuffix(filepath.Base(audioPath), filepath.Ext(audioPath))
	txtPath := filepath.Join(dir, base+".txt")
	defer os.Remove(txtPath)

	data, err := os.ReadFile(txtPath)
	if err != nil {
		return "", fmt.Errorf("baca hasil transkripsi: %w", err)
	}

	return strings.TrimSpace(string(data)), nil
}
