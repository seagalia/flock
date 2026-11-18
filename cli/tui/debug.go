package tui

import "fmt"

var debugContent string

func Debugf(format string, args ...any) {
	debugContent += fmt.Sprintf(format, args...)
}

func Debug(str string) {
	debugContent += str
}

func Debugln(str string) {
	debugContent += str + "\n"
}
