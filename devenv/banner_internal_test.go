package devenv

import (
	"bytes"
	"encoding/base64"
	"image/png"
	"io"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
	"github.com/creack/pty"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var ansiEscape = regexp.MustCompile("\x1b\\[[0-9;]*m")

var _ = Describe("Banner", func() {
	DescribeTable("should embed logos drawn on two braille dots per column",
		func(logo []byte) {
			img, err := png.Decode(bytes.NewReader(logo))
			Expect(err).ToNot(HaveOccurred())
			Expect(img.Bounds().Dx()).To(Equal(2 * logoColumns))
		},
		Entry("dark", logoDarkPNG),
		Entry("light", logoLightPNG),
	)

	DescribeTable("should embed the images of the kitty graphics protocol in the proportions of the braille logos",
		func(image, braille []byte) {
			imageConfig, err := png.DecodeConfig(bytes.NewReader(image))
			Expect(err).ToNot(HaveOccurred())
			brailleConfig, err := png.DecodeConfig(bytes.NewReader(braille))
			Expect(err).ToNot(HaveOccurred())
			Expect(imageConfig.Width).To(Equal(6 * brailleConfig.Width))
			Expect(imageConfig.Height).To(BeNumerically("~", 6*brailleConfig.Height, 6))
		},
		Entry("dark", logoDarkImagePNG, logoDarkPNG),
		Entry("light", logoLightImagePNG, logoLightPNG),
	)

	It("should draw the logo in braille within its columns", func() {
		lines := strings.Split(brailleLogo(logoDarkPNG, false), "\n")
		Expect(len(lines)).To(BeNumerically(">", 10))
		for _, line := range lines {
			Expect(utf8.RuneCountInString(line)).To(BeNumerically("<=", logoColumns))
			for _, r := range line {
				isBraille := r >= 0x2800 && r <= 0x28FF
				Expect(r == ' ' || isBraille).To(BeTrue(), "unexpected %q", r)
			}
		}
	})

	It("should write the plain logo without color", func() {
		Expect(brailleLogo(logoDarkPNG, false)).ToNot(ContainSubstring("\x1b["))
	})

	It("should color the dots with the colors of the logo", func() {
		colored := brailleLogo(logoDarkPNG, true)
		Expect(colored).To(ContainSubstring("\x1b[38;2;"))
		Expect(ansiEscape.ReplaceAllString(colored, "")).To(Equal(brailleLogo(logoDarkPNG, false)))
	})

	It("should draw the light logo with its own colors", func() {
		Expect(brailleLogo(logoLightPNG, true)).ToNot(Equal(brailleLogo(logoDarkPNG, true)))
	})

	It("should send the image to a kitty terminal in chunks, the first one placing it", func() {
		sequences := kittyImageSequences.FindAllStringSubmatch(kittyImage(logoDarkImagePNG, logoColumns), -1)
		Expect(len(sequences)).To(BeNumerically(">", 1))

		var payload strings.Builder
		for i, sequence := range sequences {
			options, chunk := sequence[1], sequence[2]
			isLast := i == len(sequences)-1
			switch {
			case i == 0:
				Expect(options).To(Equal("a=T,f=100,q=2,c=80,m=1"))
			case isLast:
				Expect(options).To(Equal("m=0"))
			default:
				Expect(options).To(Equal("m=1"))
			}
			Expect(len(chunk)).To(BeNumerically("<=", kittyImageChunk))
			payload.WriteString(chunk)
		}
		decoded, err := base64.StdEncoding.DecodeString(payload.String())
		Expect(err).ToNot(HaveOccurred())
		Expect(decoded).To(Equal(logoDarkImagePNG))
	})

	Describe("supportsKittyGraphics", func() {
		var terminal, tty *os.File

		BeforeEach(func() {
			var err error
			terminal, tty, err = pty.Open()
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(terminal.Close)
			DeferCleanup(tty.Close)
		})

		// answerQuery plays the terminal: it reads both queries then writes the answer
		answerQuery := func(answer string) {
			go func() {
				defer GinkgoRecover()
				query := make([]byte, len(kittyQuery+ansi.RequestPrimaryDeviceAttributes))
				_, err := io.ReadFull(terminal, query)
				Expect(err).ToNot(HaveOccurred())
				_, err = terminal.WriteString(answer)
				Expect(err).ToNot(HaveOccurred())
			}()
		}

		It("should be true when the terminal answers the query before the attributes", func() {
			answerQuery("\x1b_Gi=31;OK\x1b\\\x1b[?62;4;22c")
			Expect(supportsKittyGraphics(tty, tty)).To(BeTrue())
		})

		It("should be false when the terminal answers only the attributes", func() {
			answerQuery("\x1b[?62;22c")
			Expect(supportsKittyGraphics(tty, tty)).To(BeFalse())
		})

		It("should be false when the input is no terminal", func() {
			in, err := os.Open(os.DevNull)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(in.Close)
			Expect(supportsKittyGraphics(in, tty)).To(BeFalse())
		})
	})
})

var kittyImageSequences = regexp.MustCompile("\x1b_G([^;]*);([^\x1b]*)\x1b\\\\")
