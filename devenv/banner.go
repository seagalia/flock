package devenv

import (
	"bytes"
	_ "embed"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"charm.land/lipgloss/v2/compat"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
)

// The logos are the brand svgs, drawn with one braille dot per pixel of the small pngs, or shown as they are by a
// terminal of the kitty graphics protocol. Regenerated when the brand changes with:
//
//	rsvg-convert -w 160 assets/logo-dark.svg -o devenv/logo-dark.png
//	rsvg-convert -w 160 assets/logo.svg -o devenv/logo-light.png
//	rsvg-convert -w 960 assets/logo-dark.svg -o devenv/logo-dark-image.png
//	rsvg-convert -w 960 assets/logo.svg -o devenv/logo-light-image.png
var (
	//go:embed logo-dark.png
	logoDarkPNG []byte
	//go:embed logo-light.png
	logoLightPNG []byte
	//go:embed logo-dark-image.png
	logoDarkImagePNG []byte
	//go:embed logo-light-image.png
	logoLightImagePNG []byte
)

// logoColumns is the width of the logo in the terminal, a braille character holds 2x4 dots
const logoColumns = 80

// brailleDots are the bits of the dots of a braille character, by row then column
var brailleDots = [4][2]rune{{0x01, 0x08}, {0x02, 0x10}, {0x04, 0x20}, {0x40, 0x80}}

const ansiReset = "\x1b[0m"

// printBanner writes the logo, in color on a terminal unless NO_COLOR is set. It is skipped on a terminal
// narrower than the logo, where wrapped lines would break the drawing.
func printBanner(out *os.File) {
	isTerminal := term.IsTerminal(out.Fd())
	width, _, sizeErr := term.GetSize(out.Fd())
	isTooNarrow := isTerminal && sizeErr == nil && width < logoColumns
	if isTooNarrow {
		return
	}
	_, noColorSet := os.LookupEnv("NO_COLOR")
	withColor := isTerminal && !noColorSet
	if !withColor {
		fmt.Fprint(out, "\n"+brailleLogo(logoDarkPNG, false)+"\n") // nolint:errcheck
		return
	}
	// compat asked the terminal its background when the program started
	isDark := compat.HasDarkBackground
	if supportsKittyGraphics(os.Stdin, out) {
		logo := logoLightImagePNG
		if isDark {
			logo = logoDarkImagePNG
		}
		fmt.Fprint(out, "\n"+kittyImage(logo, logoColumns)+"\n") // nolint:errcheck
		return
	}
	logo := logoLightPNG
	if isDark {
		logo = logoDarkPNG
	}
	fmt.Fprint(out, "\n"+brailleLogo(logo, true)+"\n") // nolint:errcheck
}

// kittyQuery asks whether the terminal shows images of the kitty graphics protocol, about a 1x1 image it never shows
const kittyQuery = "\x1b_Gi=31,s=1,v=1,a=q,t=d,f=24;AAAA\x1b\\"

var (
	kittyOK = []byte("\x1b_Gi=31;OK")
	// the answer to the primary device attributes request, ESC [ ? 1 ; 2 c
	deviceAttributes = regexp.MustCompile(`\x1b\[\?[0-9;]*c`)
)

// supportsKittyGraphics sends the kitty query followed by a request of the primary device attributes, which every
// terminal answers, so a terminal ignoring the query answers only the second one. Terminals answer in order: the
// query is answered when its OK arrives before the attributes. A terminal answering nothing costs the timeout.
func supportsKittyGraphics(in, out *os.File) bool {
	state, err := term.MakeRaw(in.Fd())
	if err != nil {
		return false
	}
	defer term.Restore(in.Fd(), state) // nolint:errcheck
	reader, err := uv.NewCancelReader(in)
	if err != nil {
		return false
	}
	defer reader.Close() // nolint:errcheck
	timeout := time.AfterFunc(2*time.Second, func() { reader.Cancel() })
	defer timeout.Stop()

	_, err = io.WriteString(out, kittyQuery+ansi.RequestPrimaryDeviceAttributes)
	if err != nil {
		return false
	}
	var answers []byte
	buf := make([]byte, 256)
	for !deviceAttributes.Match(answers) {
		n, err := reader.Read(buf)
		if err != nil {
			return false
		}
		answers = append(answers, buf[:n]...)
	}
	return bytes.Contains(answers, kittyOK)
}

// kittyImageChunk is the most base64 a kitty graphics sequence may carry
const kittyImageChunk = 4096

// kittyImage shows the png over the given number of columns, its rows following its proportions. The png goes in
// chunks, each saying whether more follows (m=1), and q=2 keeps the terminal from answering on stdin.
func kittyImage(picture []byte, columns int) string {
	payload := base64.StdEncoding.EncodeToString(picture)
	var sequences strings.Builder
	for start := 0; start < len(payload); start += kittyImageChunk {
		end := min(start+kittyImageChunk, len(payload))
		more := "m=1"
		if end == len(payload) {
			more = "m=0"
		}
		options := []string{more}
		if start == 0 {
			options = []string{"a=T", "f=100", "q=2", fmt.Sprintf("c=%d", columns), more}
		}
		sequences.WriteString(ansi.KittyGraphics([]byte(payload[start:end]), options...))
	}
	return sequences.String()
}

// brailleLogo draws the png with a braille dot per opaque pixel, each character in the average color of its dots
func brailleLogo(logo []byte, withColor bool) string {
	img, err := png.Decode(bytes.NewReader(logo))
	if err != nil {
		// the logos are embedded pngs, the specs decode them
		panic(err)
	}
	bounds := img.Bounds()
	lines := make([]string, 0)
	for y := bounds.Min.Y; y < bounds.Max.Y; y += 4 {
		var line strings.Builder
		for x := bounds.Min.X; x < bounds.Max.X; x += 2 {
			char, dotsColor := brailleChar(img, x, y)
			if char == ' ' || !withColor {
				line.WriteRune(char)
				continue
			}
			fmt.Fprintf(&line, "\x1b[38;2;%d;%d;%dm%c%s", dotsColor.R, dotsColor.G, dotsColor.B, char, ansiReset)
		}
		lines = append(lines, strings.TrimRight(line.String(), " "))
	}
	return strings.Trim(strings.Join(lines, "\n"), "\n")
}

// brailleChar gives the braille character of the 2x4 pixels at x, y and the average color of its dots,
// a space when no pixel is opaque
func brailleChar(img image.Image, x, y int) (rune, color.NRGBA) {
	var dots rune
	var red, green, blue, count int
	for row := range 4 {
		for column := range 2 {
			pixel := color.NRGBAModel.Convert(img.At(x+column, y+row)).(color.NRGBA)
			isOpaque := image.Pt(x+column, y+row).In(img.Bounds()) && pixel.A >= 0x80
			if !isOpaque {
				continue
			}
			dots |= brailleDots[row][column]
			red += int(pixel.R)
			green += int(pixel.G)
			blue += int(pixel.B)
			count++
		}
	}
	if count == 0 {
		return ' ', color.NRGBA{}
	}
	average := color.NRGBA{R: uint8(red / count), G: uint8(green / count), B: uint8(blue / count), A: 0xff}
	return 0x2800 + dots, average
}
