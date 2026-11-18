package cli

import "github.com/logrusorgru/aurora/v4"

var auroraColorMap = map[uint]aurora.Color{
	0: aurora.BlackFg,
	1: aurora.RedFg,
	2: aurora.GreenFg,
	3: aurora.YellowFg,
	4: aurora.BlueFg,
	5: aurora.MagentaFg,
	6: aurora.CyanFg,
}

type AutoColor struct {
	colorMap     map[string]aurora.Color
	currentColor uint
}

func NewAutoColor() *AutoColor {
	return &AutoColor{
		colorMap:     map[string]aurora.Color{},
		currentColor: 1,
	}
}

func (a *AutoColor) GetColor(id string) aurora.Color {
	if _, ok := a.colorMap[id]; !ok {
		a.colorMap[id] = auroraColorMap[a.currentColor]
		a.currentColor++
		a.currentColor = a.currentColor % 7
	}
	return a.colorMap[id]
}
