package grip

import (
	"github.com/k0kubun/go-ansi"
	"github.com/schollz/progressbar/v3"
)

func NewProgressBar(size int, description string) *progressbar.ProgressBar {
	return progressbar.NewOptions(size,
		progressbar.OptionFullWidth(),
		progressbar.OptionSetWriter(ansi.NewAnsiStdout()),
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
