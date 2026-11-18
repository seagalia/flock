package tuicommon

import (
	"errors"
	"image/color"
	"net/url"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/compat"
	"connectrpc.com/connect/v2"
	"github.com/seagalia/flock/cli/common"
)

type TickMsg time.Time

type ReloadMsg bool

var DefaultHighlight = lipgloss.NewStyle().Background(lipgloss.Color("#334")).Foreground(lipgloss.Color("#ffffff"))

// The colors of the Flock brand, a darker shade for light terminals and a lighter one for dark terminals. compat
// queries the terminal background once, when the program starts.
var (
	Navy   = adaptive("#073B74", "#87ACEC")
	Blue   = adaptive("#375F99", "#87ACEC")
	Green  = adaptive("#006C45", "#35B77D")
	Orange = adaptive("#C5542B", "#F4774B")
	Red    = adaptive("#C8412B", "#FF8B64")
	// Yellow is not a brand color, it sets a name or an error code apart in a sentence.
	Yellow = lipgloss.Color("#cca721")
	Grey   = lipgloss.Color("#b0b0b0")
	Gray   = Grey
)

func adaptive(light, dark string) color.Color {
	return compat.AdaptiveColor{Light: lipgloss.Color(light), Dark: lipgloss.Color(dark)}
}

func RpcErrorFormat(err error) string {
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		return err.Error()
	}

	messg, _ := url.QueryUnescape(connectErr.Message()) // nolint:errcheck
	sb := strings.Builder{}
	sb.WriteString(lipgloss.NewStyle().Foreground(Blue).Render(messg))
	sb.WriteString(" (Code: ")
	sb.WriteString(lipgloss.NewStyle().Foreground(Yellow).Render(common.CodeName(connectErr.Code())))
	sb.WriteString(")")
	return sb.String()
}

func FormatToWidth(s string, width int) string {
	if lipgloss.Width(s) < width {
		return s
	}
	content := ""
	for _, r := range s {
		if lipgloss.Width(content+string(r)) >= width {
			content += "\n"
		}
		content += string(r)

	}
	return content
}
