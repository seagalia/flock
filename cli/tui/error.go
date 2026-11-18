package tui

import "fmt"

var errorContent string

func Errorf(format string, args ...any) {
	errorContent += fmt.Sprintf(format, args...)
}

func Error(content any) {
	if errContent, ok := content.(error); ok {
		errorContent += errContent.Error()
		return
	}
	errorContent += fmt.Sprint(content)
}

func Errorln(content any) {
	if errContent, ok := content.(error); ok {
		errorContent += errContent.Error() + "\n"
		return
	}
	errorContent += fmt.Sprint(content) + "\n"
}
