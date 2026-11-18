package overview

import (
	"fmt"
	"image/color"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/seagalia/flock/cli/tui/tuicommon"
	"github.com/seagalia/flock/sdk/flock/api/config/agent/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	svclookup "github.com/seagalia/flock/sdk/flock/services/lookup/v1"
)

const (
	backendIndent  = "    "
	endpointIndent = "      "
	maxNameWidth   = 40
)

var (
	dimStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	boldStyle     = lipgloss.NewStyle().Bold(true)
	instanceStyle = lipgloss.NewStyle().Bold(true).Foreground(tuicommon.Blue)
	backendStyle  = lipgloss.NewStyle().Bold(true).Foreground(tuicommon.Blue)
	timeStyle     = lipgloss.NewStyle().Foreground(tuicommon.Blue)
	errorStyle    = lipgloss.NewStyle().Foreground(tuicommon.Red)
	selectedStyle = tuicommon.DefaultHighlight
)

func colored(textColor color.Color, text string) string {
	return lipgloss.NewStyle().Foreground(textColor).Render(text)
}

// healthColor is green when everything is healthy, red when nothing is, orange in between
func healthColor(healthy, total int) color.Color {
	switch {
	case total == 0:
		return tuicommon.Gray
	case healthy == total:
		return tuicommon.Green
	case healthy == 0:
		return tuicommon.Red
	}
	return tuicommon.Orange
}

// pad fills s with spaces up to width, ignoring the color codes of s
func pad(s string, width int) string {
	return s + strings.Repeat(" ", max(0, width-lipgloss.Width(s)))
}

// vsHealth is the state column of a virtual server: its healthy endpoints over all its endpoints
func vsHealth(backendUuids []string, endpoints map[string][]*backend.EndpointStat) string {
	if len(backendUuids) == 0 {
		return dimStyle.Render("no backend set")
	}
	healthy, total := 0, 0
	for _, backendUuid := range backendUuids {
		for _, endpoint := range endpoints[backendUuid] {
			total++
			if endpoint.GetHealthy() {
				healthy++
			}
		}
	}
	if total == 0 {
		return dimStyle.Render("no endpoint reported")
	}
	return colored(healthColor(healthy, total), fmt.Sprintf("● %d/%d healthy", healthy, total))
}

func agentStateWord(status agent.Status) string {
	switch status {
	case agent.Status_OK:
		return colored(tuicommon.Green, "Started")
	case agent.Status_ERRORED:
		return colored(tuicommon.Red, "Stopped")
	case agent.Status_WARNING:
		return colored(tuicommon.Orange, "Warning")
	}
	return colored(tuicommon.Gray, "Unknown")
}

// agentsLine summarizes the agents of the instance on one line.
// A sidecar agent counts as one server with its components, a proxy agent is only its components.
func agentsLine(statuses []*svclookup.GetAgentStatusResponse) string {
	unit := "components"
	alive, total := 0, 0
	details := make([]string, 0)
	for _, status := range statuses {
		if sidecar := status.GetAgentStatus().GetSidecarAgentState(); sidecar != nil {
			unit = "agents"
			total++
			if sidecar.GetAgentState().GetStatus() == agent.Status_OK {
				alive++
			}
			details = append(details, sidecarDetail(sidecar))
			continue
		}
		for _, component := range status.GetAgentStatus().GetProxyAgentState().GetComponentStates() {
			total++
			if component.GetStatus() == agent.Status_OK {
				alive++
			}
			details = append(details, boldStyle.Render(component.GetName())+" "+agentStateWord(component.GetStatus()))
		}
	}
	if total == 0 {
		return dimStyle.Render("○ no agent status reported")
	}
	summary := colored(healthColor(alive, total), "●") + fmt.Sprintf(" %d/%d %s alive", alive, total, unit)
	return summary + dimStyle.Render(" · ") + strings.Join(details, dimStyle.Render(" · "))
}

func sidecarDetail(sidecar *agent.SidecarAgentState) string {
	detail := boldStyle.Render(sidecar.GetAgentState().GetName()) + " " + agentStateWord(sidecar.GetAgentState().GetStatus())
	components := sidecar.GetComponentStates()
	if len(components) == 0 {
		return detail
	}
	healthy := 0
	for _, component := range components {
		if component.GetStatus() == agent.Status_OK {
			healthy++
		}
	}
	ratio := colored(healthColor(healthy, len(components)), fmt.Sprintf("%d/%d", healthy, len(components)))
	return detail + " " + ratio + dimStyle.Render(" components")
}

// tableLayout holds the column widths of the virtual servers table, sized on its content
type tableLayout struct {
	name    int
	kind    int
	address int
}

func newTableLayout(vss []*VsInfo) tableLayout {
	layout := tableLayout{name: len("NAME"), kind: len("TYPE"), address: len("ADDRESS")}
	for _, vsInfo := range vss {
		layout.name = max(layout.name, lipgloss.Width(vsInfo.GetName()))
		layout.kind = max(layout.kind, len(vsInfo.GetType()))
		for _, address := range vsInfo.Addresses() {
			layout.address = max(layout.address, len(address))
		}
	}
	layout.name = min(layout.name, maxNameWidth)
	return layout
}

func (l tableLayout) header() string {
	return dimStyle.Render("  " + l.columns("NAME", "TYPE", "ADDRESS") + "STATE")
}

func (l tableLayout) rule(width int) string {
	ruleWidth := lipgloss.Width("  "+l.columns("", "", "")) + len("● 99/99 healthy")
	if width > 0 {
		ruleWidth = min(ruleWidth, width)
	}
	return dimStyle.Render(strings.Repeat("─", ruleWidth))
}

func (l tableLayout) columns(name, kind, address string) string {
	return pad(name, l.name) + "  " + pad(kind, l.kind) + "  " + pad(address, l.address) + "  "
}

// rows gives the lines of a virtual server: the first holds every column,
// the next ones the other addresses when several VIPs announce it
func (l tableLayout) rows(vsInfo *VsInfo, expanded, selected bool, health string) []string {
	marker := "▸ "
	if expanded {
		marker = "▾ "
	}
	addresses := vsInfo.Addresses()
	firstAddress := ""
	if len(addresses) > 0 {
		firstAddress = addresses[0]
	}
	name := ansi.Truncate(vsInfo.GetName(), l.name, "…")
	left := marker + l.columns(name, vsInfo.GetType(), firstAddress)
	if selected {
		left = selectedStyle.Render(left)
	}
	lines := make([]string, 0, len(addresses)+1)
	lines = append(lines, left+health)
	for _, address := range addresses[min(1, len(addresses)):] {
		lines = append(lines, "  "+l.columns("", "", address))
	}
	return lines
}

// backendLines shows the backends of an expanded virtual server, each with its endpoints
func backendLines(vsInfo *VsInfo, backends map[string]*BackendInfo, endpoints map[string][]*backend.EndpointStat) []string {
	backendUuids := vsInfo.GetBackends()
	if len(backendUuids) == 0 {
		return []string{backendIndent + dimStyle.Render("no backend set")}
	}
	lines := make([]string, 0)
	for _, backendUuid := range backendUuids {
		info, read := backends[backendUuid]
		if !read {
			lines = append(lines, backendIndent+dimStyle.Render("backend "+backendUuid+" loading…"))
			continue
		}
		if info.err != nil {
			lines = append(lines, backendIndent+"backend "+backendUuid+" "+errorStyle.Render(tuicommon.RpcErrorFormat(info.err)))
			continue
		}
		description := strings.Join([]string{
			info.EndpointConfig(),
			info.Policy(),
			"hc " + info.HealthcheckType(),
			"proxy protocol " + info.ProxyProtocol(),
		}, " · ")
		lines = append(lines, backendIndent+dimStyle.Render("backend ")+backendStyle.Render(info.GetName())+"  "+dimStyle.Render(description))
		lines = append(lines, endpointLines(info, endpoints[backendUuid])...)
	}
	return lines
}

// endpointLines lists the endpoints reported by the agents, then the static addresses no agent reported yet
func endpointLines(info *BackendInfo, reported []*backend.EndpointStat) []string {
	weights := make(map[string]uint32)
	addressWidth := 0
	for _, address := range info.StaticAddresses() {
		weights[address.Format()] = address.GetLoadBalancingWeight()
		addressWidth = max(addressWidth, len(address.Format()))
	}
	for _, endpoint := range reported {
		addressWidth = max(addressWidth, len(endpoint.GetIpAddr().Format()))
	}

	lines := make([]string, 0)
	seen := make(map[string]bool)
	for _, endpoint := range reported {
		address := endpoint.GetIpAddr().Format()
		seen[address] = true
		state := colored(tuicommon.Green, "● ") + pad(address, addressWidth) + "  " + colored(tuicommon.Green, "healthy")
		if !endpoint.GetHealthy() {
			state = colored(tuicommon.Red, "● ") + pad(address, addressWidth) + "  " + colored(tuicommon.Red, "unhealthy")
		}
		if weight := weights[address]; weight != 0 {
			state += dimStyle.Render("  weight " + strconv.Itoa(int(weight)))
		}
		if lastError := endpoint.GetLastError().GetValue(); lastError != "" {
			state += "  " + errorStyle.Render(lastError)
		}
		lines = append(lines, endpointIndent+state)
	}
	for _, address := range info.StaticAddresses() {
		if seen[address.Format()] {
			continue
		}
		lines = append(lines, endpointIndent+dimStyle.Render("○ "+pad(address.Format(), addressWidth)+"  no status"))
	}
	if len(lines) == 0 {
		return []string{endpointIndent + dimStyle.Render("no endpoint reported")}
	}
	return lines
}
