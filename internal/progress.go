package grip

import (
	"fmt"
	"os"

	"github.com/alexjoedt/grip/internal/logger"
	"github.com/schollz/progressbar/v3"
)

// progressVisible reports whether progress bars are drawn: stderr is a
// terminal and output is not quiet.
func progressVisible() bool {
	fi, err := os.Stderr.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0 && !logger.IsQuiet()
}

func NewProgressBar(size int, description string) *progressbar.ProgressBar {
	return progressbar.NewOptions(size,
		progressbar.OptionSetVisibility(progressVisible()),
		progressbar.OptionFullWidth(),
		progressbar.OptionSetWriter(os.Stderr),
		progressbar.OptionEnableColorCodes(true),
		progressbar.OptionShowBytes(true),
		progressbar.OptionSetDescription(description),
		progressbar.OptionSetTheme(progressbar.Theme{
			Saucer:        "[green]=[reset]",
			SaucerHead:    "[green]>[reset]",
			SaucerPadding: " ",
			BarStart:      "[",
			BarEnd:        "]",
		}))
}

// endProgressBar ends the line of a drawn progress bar.
func endProgressBar() {
	if progressVisible() {
		fmt.Fprintln(os.Stderr)
	}
}
