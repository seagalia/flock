// Code from github.com/andreazorzetto/yh/highlight made by Andrea Zorzetto but using messages

package highlight

import (
	"bufio"
	"fmt"
	msg "github.com/ArthurHlt/messages"
	"io"
	"strings"
)

func Highlight(r io.Reader) (string, error) {
	foundChompingIndicator := false
	indentationSpacesBeforeComment := 0

	scanner := bufio.NewScanner(r)

	var buf strings.Builder

	for scanner.Scan() {
		if scanner.Text() == "EOF" {
			break
		}

		if err := scanner.Err(); err != nil {
			return "", err
		}

		l := yamlLine{raw: scanner.Text()}

		if foundChompingIndicator && (l.indentationSpaces() > indentationSpacesBeforeComment) {
			// Found multiline comment or configmap, not treated as YAML at all
			buf.WriteString(multiline(l))

		} else if l.isKeyValue() {
			if l.isComment() {
				buf.WriteString(comment(l))
			} else if l.valueIsNumberOrIP() {
				buf.WriteString(keyNumberOrIP(l))

			} else if l.valueIsBoolean() {
				buf.WriteString(keyBool(l))

			} else {
				buf.WriteString(keyValue(l))
			}

			if l.valueContainsChompingIndicator() {
				foundChompingIndicator = true

				indentationSpacesBeforeComment = l.indentationSpaces()

			} else {
				foundChompingIndicator = false
			}

		} else if !l.isEmptyLine() {
			if l.isUrl() {
				buf.WriteString(url(l))
			} else if l.isComment() {
				buf.WriteString(comment(l))
			} else if l.isElementOfList() {
				buf.WriteString(listElement(l))
			} else {
				buf.WriteString(invalidLine(l))
			}

			foundChompingIndicator = false

		} else if l.isEmptyLine() {
			buf.WriteString(l.raw + "\n")
		}

	}

	return buf.String(), nil
}

func keyValue(l yamlLine) string {
	return fmt.Sprintf("%v: %v\n", msg.BrightRed(l.key), msg.Yellow(l.value))
}

func keyNumberOrIP(l yamlLine) string {
	return fmt.Sprintf("%v: %v\n", msg.BrightRed(l.key), msg.Blue(l.value))
}

func keyBool(l yamlLine) string {
	return fmt.Sprintf("%v: %v\n", msg.BrightRed(l.key), msg.Blue(l.value))
}

func comment(l yamlLine) string {
	return fmt.Sprintf("%v\n", msg.Gray(13, l.raw))
}

func listElement(l yamlLine) string {
	return fmt.Sprintf("%v\n", msg.Yellow(l.raw))
}

func invalidLine(l yamlLine) string {
	return fmt.Sprintf("%v\n", msg.Black(l.raw).BgBrightRed())
}

func multiline(l yamlLine) string {
	return fmt.Sprintf("%v\n", msg.Gray(20-1, l.raw))
}
func url(l yamlLine) string {
	return fmt.Sprintf("%v\n", msg.Yellow(l.raw))
}
