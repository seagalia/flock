package status

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
	"github.com/evertras/bubble-table/table"
	zone "github.com/lrstanley/bubblezone/v2"
	"github.com/seagalia/flock/cli/common"
	"github.com/seagalia/flock/cli/tui/components/tabs"
	"github.com/seagalia/flock/cli/tui/tuicommon"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
	svclookup "github.com/seagalia/flock/sdk/flock/services/lookup/v1"
)

type KeyMapStatus struct {
	LineUp         key.Binding
	LineDown       key.Binding
	Left           key.Binding
	Right          key.Binding
	FilterInstance key.Binding
	NextTab        key.Binding
	PrevTab        key.Binding
	FilterVs       key.Binding
}

// ShortHelp implements the KeyMap interface.
func (km KeyMapStatus) ShortHelp() []key.Binding {
	return []key.Binding{
		km.LineUp, km.LineDown, km.Left, km.Right, km.FilterInstance,
		km.FilterVs, km.NextTab, km.PrevTab,
	}
}

// FullHelp implements the KeyMap interface.
func (km KeyMapStatus) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{km.LineUp, km.LineDown, km.Left, km.Right},
		{km.FilterInstance, km.FilterVs, km.NextTab, km.PrevTab},
	}
}

var keyMapStatus = KeyMapStatus{
	LineUp: key.NewBinding(
		key.WithKeys("up", "k"),
		key.WithHelp("↑/k", "up"),
	),
	LineDown: key.NewBinding(
		key.WithKeys("down", "j"),
		key.WithHelp("↓/j", "down"),
	),
	Left: key.NewBinding(
		key.WithKeys("left", "h", "pgup"),
		key.WithHelp("←/h/pgup", "previous table page"),
	),
	Right: key.NewBinding(
		key.WithKeys("right", "l", "pgdown"),
		key.WithHelp("→/l/pgdown", "next table page"),
	),
	FilterInstance: key.NewBinding(
		key.WithKeys("/"),
		key.WithHelp("/", "select other instance"),
	),
	NextTab: key.NewBinding(
		key.WithKeys("tab"),
		key.WithHelp("tab", "next agent"),
	),
	PrevTab: key.NewBinding(
		key.WithKeys("shift+tab"),
		key.WithHelp("shift+tab", "previous agent"),
	),
	FilterVs: key.NewBinding(
		key.WithKeys("s"),
		key.WithHelp("s", "press it and start typing for filtering and use escape when finish"),
	),
}

type VipBackendsInstance struct {
	Instance   *instance.Instance
	VipBackend map[string][]string
}

var baseStyle = lipgloss.NewStyle().
	BorderStyle(lipgloss.NormalBorder()).
	BorderForeground(lipgloss.Color("240"))

type TableEndpoints struct {
	formSelectInstance   *huh.Form
	tableVS              table.Model
	tableEndpointStat    table.Model
	tabs                 *tabs.Tabs
	responses            []*svclookup.StatusResponse
	vipBackendsInstances []*VipBackendsInstance
	showForm             bool
	instanceChose        *instance.Instance
	help                 help.Model
	timeUpdate           time.Time
	errorAgentMsg        string
	colorTime            bool
	debug                string
}

func NewTableEndpoints(responses []*svclookup.StatusResponse, vipBackendInstances []*VipBackendsInstance) *TableEndpoints {
	columnsVs := []table.Column{
		table.NewColumn("vs", "IP:PORT/Virtual Server", 50).WithFiltered(true),
		table.NewColumn("state", "State", 25),
	}
	columnEndpoints := []table.Column{
		table.NewColumn("endpoint", "Endpoint", 25),
		table.NewColumn("state", "State", 25),
		table.NewColumn("error", "Error", 50),
	}
	h := help.New()
	h.ShowAll = true
	keyMap := table.DefaultKeyMap()
	keyMap.Filter.SetKeys("s")
	return &TableEndpoints{
		formSelectInstance: nil,
		tableVS: table.New(columnsVs).
			WithBaseStyle(baseStyle).
			Focused(true).
			Filtered(true).
			BorderRounded().
			WithPageSize(15).
			WithKeyMap(keyMap).
			HighlightStyle(tuicommon.DefaultHighlight),
		tableEndpointStat: table.New(columnEndpoints).
			WithBaseStyle(baseStyle).
			BorderRounded().
			HighlightStyle(tuicommon.DefaultHighlight),
		responses:            responses,
		vipBackendsInstances: vipBackendInstances,
		showForm:             false,
		help:                 h,
		timeUpdate:           time.Now(),
	}
}

func (m *TableEndpoints) Init() tea.Cmd {
	m.makeTabsAgent()
	m.makeForm()
	return nil
}

func (m *TableEndpoints) Update(tMsg tea.Msg) tea.Cmd {
	m.makeTabsAgent()
	tMsg, cmds := m.handleInput(tMsg)
	var cmd tea.Cmd
	var form huh.Model
	form, cmd = m.formSelectInstance.Update(tMsg)
	if f, ok := form.(*huh.Form); ok {
		m.formSelectInstance = f
		cmds = append(cmds, cmd)
	}

	if m.tabs != nil {
		m.tabs, cmd = m.tabs.Update(tMsg)
		cmds = append(cmds, cmd)
	}

	vipBackend := m.chooseInstance()
	rowsVs := m.vsRows(vipBackend)
	m.tableVS = m.tableVS.WithRows(rowsVs)
	m.tableVS, cmd = m.tableVS.Update(tMsg)
	cmds = append(cmds, cmd)

	// the endpoints shown are those of the virtual server highlighted in the table just updated
	m.tableEndpointStat = m.tableEndpointStat.WithRows(m.endpointRows(vipBackend, rowsVs))
	m.tableEndpointStat, cmd = m.tableEndpointStat.Update(tMsg)
	cmds = append(cmds, cmd)

	return tea.Batch(cmds...)
}

// handleInput reacts to the keys and clicks of the page. It gives back the message the components still have to see,
// nil when the key opened or closed the instance form.
func (m *TableEndpoints) handleInput(tMsg tea.Msg) (tea.Msg, []tea.Cmd) {
	cmds := make([]tea.Cmd, 0)
	switch tMsgRef := tMsg.(type) {
	case tea.KeyPressMsg:
		switch tMsgRef.String() {
		case "d":
			cmds = append(cmds, tea.Printf("%v\n", m.responses))
		case "esc":
		case "/":
			tMsg = nil
			m.tableVS = m.tableVS.Focused(m.showForm)
			m.showForm = !m.showForm
			if m.showForm {
				m.makeForm()
				cmds = append(cmds, m.formSelectInstance.Init())
			}
		case "enter":
			if m.showForm {
				m.tableVS = m.tableVS.Focused(true)
				m.showForm = false
			}
		}
	case tea.MouseReleaseMsg:
		if tMsgRef.Button != tea.MouseLeft {
			break
		}
		if zone.Get("tableVS").InBounds(tMsgRef) {
			m.tableVS = m.tableVS.Focused(true)

		}
		if zone.Get("tabsAgent").InBounds(tMsgRef) {
			m.tableVS = m.tableVS.Focused(true)
		}
	}
	return tMsg, cmds
}

// chooseInstance sets the instance shown, the one picked in the form or else the last one shown, and gives the
// virtual servers of its backends.
func (m *TableEndpoints) chooseInstance() map[string][]string {
	var instanceUuid string
	if m.instanceChose == nil {
		instanceUuid = m.vipBackendsInstances[0].Instance.GetUUID()
	} else {
		instanceUuid = m.instanceChose.GetUUID()
	}
	if m.formSelectInstance.GetString("instance") != "" {
		instanceUuid = m.formSelectInstance.GetString("instance")
	}
	var vipBackend map[string][]string
	for _, vipBackendInstance := range m.vipBackendsInstances {
		if vipBackendInstance.Instance.GetUUID() == instanceUuid {
			vipBackend = vipBackendInstance.VipBackend
			m.instanceChose = vipBackendInstance.Instance
		}
	}
	return vipBackend
}

func (m *TableEndpoints) isShown(statusResponse *svclookup.StatusResponse) bool {
	isOtherAgent := m.tabs != nil && statusResponse.AgentUuid != m.tabs.GetActiveTab().Key
	return !isOtherAgent && m.instanceChose.GetUUID() == statusResponse.GetInstanceUuid()
}

// isAgentError tells a response about the agent itself, which holds no resource.
func isAgentError(statusResponse *svclookup.StatusResponse) bool {
	return statusResponse.GetState() == svclookup.StatusResponse_ERRORED &&
		statusResponse.ErroredResource == nil && statusResponse.PreviousResource == nil
}

// vsRows gives one row per virtual server with the health of its backend, and collects the errors of the agents.
func (m *TableEndpoints) vsRows(vipBackend map[string][]string) []table.Row {
	rowsVs := make([]table.Row, 0)
	m.errorAgentMsg = ""
	for _, statusResponse := range m.responses {
		if !m.isShown(statusResponse) {
			continue
		}
		if isAgentError(statusResponse) {
			m.errorAgentMsg += fmt.Sprintf(
				"Agent %s in error: %s\n",
				lipgloss.NewStyle().Foreground(tuicommon.Orange).Render(statusResponse.AgentUuid),
				tuicommon.RpcErrorFormat(common.RpcErrorFromString(statusResponse.Error)),
			)
			continue
		}
		endpointStats := &backend.BackendEndpointStats{}
		err := statusResponse.GetPreviousResource().UnmarshalTo(endpointStats)
		if err != nil {
			panic(err)
		}
		healthy := backendHealthCell(endpointStats)

		vsExp, ok := vipBackend[endpointStats.GetBackendUuid()]
		if !ok {
			vsExp = []string{"Backend UUID: " + endpointStats.GetBackendUuid()}
		}
		for _, vsInfo := range vsExp {
			rowsVs = append(rowsVs, table.NewRow(table.RowData{
				"vs":    vsInfo,
				"state": healthy,
			}))
		}
	}
	slices.SortFunc(rowsVs, func(a, b table.Row) int {
		return cmp.Compare(a.Data["vs"].(string), b.Data["vs"].(string))
	})
	return rowsVs
}

func backendHealthCell(endpointStats *backend.BackendEndpointStats) table.StyledCell {
	healthyStyle := lipgloss.NewStyle().
		Foreground(tuicommon.Green)
	healthyText := "Alive"
	if !endpointStats.GetHealthy() {
		healthyStyle = healthyStyle.Foreground(tuicommon.Red)
		healthyText = "Dead"
	}
	for _, endpointStat := range endpointStats.GetEndpointStats() {
		if !endpointStat.GetHealthy() && endpointStats.GetHealthy() {
			healthyStyle = healthyStyle.Foreground(tuicommon.Orange)
			healthyText = "Warning"
		}
	}
	return table.NewStyledCell(healthyText, healthyStyle)
}

// endpointRows gives the endpoints of the virtual server highlighted in the table, the first one when none is.
func (m *TableEndpoints) endpointRows(vipBackend map[string][]string, rowsVs []table.Row) []table.Row {
	mapRowsEndpoints := make(map[string]table.Row)
	for _, statusResponse := range m.responses {
		if !m.isShown(statusResponse) || isAgentError(statusResponse) {
			continue
		}
		endpointStats := &backend.BackendEndpointStats{}
		err := statusResponse.GetPreviousResource().UnmarshalTo(endpointStats)
		if err != nil {
			panic(err)
		}

		vsExp, ok := vipBackend[endpointStats.GetBackendUuid()]
		if !ok {
			vsExp = []string{"Backend UUID: " + endpointStats.GetBackendUuid()}
		}
		for _, endpointStat := range endpointStats.GetEndpointStats() {
			healthyStyle := lipgloss.NewStyle().
				Foreground(tuicommon.Green)
			healthyText := "Healthy"
			if !endpointStat.GetHealthy() {
				healthyStyle = healthyStyle.Foreground(tuicommon.Red)
				healthyText = "Unhealthy"
			}
			healthy := table.NewStyledCell(healthyText, healthyStyle)
			for _, vsInfo := range vsExp {
				filterVs, ok := m.tableVS.HighlightedRow().Data["vs"]
				if !ok {
					filterVs = rowsVs[0].Data["vs"]
				}
				if vsInfo != filterVs {
					continue
				}
				if _, ok := mapRowsEndpoints[endpointStat.GetIpAddr().Format()]; !ok {
					mapRowsEndpoints[endpointStat.GetIpAddr().Format()] = table.NewRow(table.RowData{
						"endpoint": endpointStat.GetIpAddr().Format(),
						"state":    healthy,
						"error":    endpointStat.GetLastError().GetValue(),
					})
				}

			}
		}
	}
	rowsEndpoints := make([]table.Row, 0, len(mapRowsEndpoints))
	for _, row := range mapRowsEndpoints {
		rowsEndpoints = append(rowsEndpoints, row)
	}

	slices.SortFunc(rowsEndpoints, func(a, b table.Row) int {
		return cmp.Compare(a.Data["endpoint"].(string), b.Data["endpoint"].(string))
	})
	return rowsEndpoints
}

func (m *TableEndpoints) View() string {
	pre := ""
	if m.debug != "" {
		pre += m.debug + "\n"
	}
	if m.showForm {
		pre += m.formSelectInstance.View()
	}
	content := m.errorAgentMsg
	if m.tableVS.TotalRows() > 0 {
		content += m.tableVS.View()
	}
	if m.tabs != nil {
		m.tabs.SetContent(content)
		content = m.tabs.View()
	}

	sb := strings.Builder{}
	sb.WriteString("Showing state of instance '")
	sb.WriteString(
		lipgloss.NewStyle().
			Foreground(tuicommon.Yellow).
			Render(m.instanceChose.GetName()),
	)
	sb.WriteString("' (updated at ")

	styleTime := lipgloss.NewStyle()
	if m.colorTime {
		styleTime = styleTime.Foreground(tuicommon.Grey)
	} else {
		styleTime = styleTime.Foreground(tuicommon.Blue)
	}
	sb.WriteString(styleTime.Render(m.timeUpdate.Format(time.DateTime)))
	sb.WriteString("):\n")
	content = lipgloss.JoinVertical(lipgloss.Left, sb.String(), content)

	if m.tableEndpointStat.TotalRows() > 0 && !m.tableEndpointStat.GetIsFilterActive() {
		sb = strings.Builder{}
		sb.WriteString("TableEndpoints information for '")
		sb.WriteString(
			lipgloss.NewStyle().
				Foreground(tuicommon.Blue).
				Render(fmt.Sprint(m.tableVS.HighlightedRow().Data["vs"])),
		)
		sb.WriteString("':\n")
		content = lipgloss.JoinVertical(lipgloss.Left, content, sb.String(), m.tableEndpointStat.View())

	} else {
		content = lipgloss.JoinVertical(lipgloss.Left, content, "No endpoints information to show")
	}

	if pre != "" {
		content = lipgloss.JoinVertical(lipgloss.Left, pre, content)
	}
	content = lipgloss.JoinVertical(lipgloss.Left, content, m.help.View(keyMapStatus))

	return content
}

func (m *TableEndpoints) makeForm() {
	opts := make([]huh.Option[string], 0, len(m.vipBackendsInstances))
	for _, vipBackendInstance := range m.vipBackendsInstances {
		opts = append(opts,
			huh.NewOption(vipBackendInstance.Instance.GetName(), vipBackendInstance.Instance.GetUUID()),
		)
	}
	selectUuid := m.instanceChose.GetUUID()
	m.formSelectInstance = huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Key("instance").
				Options(opts...).
				Title("Chose instance to look at").
				Description("Select the instance you want to see the status of").
				Value(&selectUuid),
		),
	).WithTheme(tuicommon.FormTheme)
}

func (m *TableEndpoints) makeTabsAgent() {
	agentsUuid := make(map[string]bool, 0)
	for _, statusResponse := range m.responses {
		agentsUuid[statusResponse.AgentUuid] = true
	}
	agents := make([]*tabs.Elem, 0, len(agentsUuid))
	for agent := range agentsUuid {
		agents = append(agents, &tabs.Elem{
			Key:   agent,
			Value: "Agent " + lipgloss.NewStyle().Italic(true).Bold(true).Render(agent),
		})
	}
	slices.SortFunc(agents, func(a, b *tabs.Elem) int {
		return cmp.Compare(a.Key, b.Key)
	})

	if m.tabs != nil {
		m.tabs.SetTabs(agents)
		return
	}
	var tabsModel *tabs.Tabs
	if len(agents) == 1 {
		return
	}
	tabsModel = tabs.New(agents)
	m.tabs = tabsModel
}

func (m *TableEndpoints) SetData(responses []*svclookup.StatusResponse, vipBackendInstances []*VipBackendsInstance) {
	m.responses = responses
	m.vipBackendsInstances = vipBackendInstances
	m.timeUpdate = time.Now()
	m.colorTime = !m.colorTime
}
