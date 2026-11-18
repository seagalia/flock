package overview

import (
	"context"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/seagalia/flock/cli/tui"
	"github.com/seagalia/flock/cli/tui/tuicommon"
	"github.com/seagalia/flock/sdk/clients"
	"github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
)

const pageMove = 10

type keyMap struct {
	Up          key.Binding
	Down        key.Binding
	PageUp      key.Binding
	PageDown    key.Binding
	Expand      key.Binding
	Filter      key.Binding
	ClearFilter key.Binding
	Instance    key.Binding
	Reload      key.Binding
	Help        key.Binding
	Quit        key.Binding
}

func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Up, k.Down, k.Expand, k.Filter, k.Instance, k.Reload, k.Help, k.Quit}
}

func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.PageUp, k.PageDown},
		{k.Expand, k.Filter, k.ClearFilter},
		{k.Instance, k.Reload, k.Help, k.Quit},
	}
}

var keys = keyMap{
	Up:          key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
	Down:        key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
	PageUp:      key.NewBinding(key.WithKeys("pgup"), key.WithHelp("pgup", "page up")),
	PageDown:    key.NewBinding(key.WithKeys("pgdown"), key.WithHelp("pgdown", "page down")),
	Expand:      key.NewBinding(key.WithKeys("enter", "space"), key.WithHelp("↵/space", "expand")),
	Filter:      key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "filter")),
	ClearFilter: key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "clear filter")),
	Instance:    key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "instance")),
	Reload:      key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "reload")),
	Help:        key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
	// the app quits on q, the binding is only shown in the help
	Quit: key.NewBinding(key.WithKeys("q"), key.WithHelp("q", "quit")),
}

// Overview shows the virtual servers announced by an instance with the health of their endpoints,
// a virtual server expands in place to show its backends and endpoints
type Overview struct {
	// ctx is the one of the command, loadCtx is the one of the current load, a reload cancels it
	ctx             context.Context
	loadCtx         context.Context
	cancelLoad      context.CancelFunc
	client          *clients.Client
	instancesLoaded bool
	instances       []*instance.Instance
	inst            *instance.Instance
	data            *instanceData
	loading         bool
	loadErr         error
	updatedAt       time.Time
	// backends by uuid, read when a virtual server is expanded
	backends map[string]*BackendInfo
	// expanded virtual servers by uuid
	expanded map[string]bool
	cursor   int
	// scroll is the first line of the table body on screen
	scroll         int
	filter         textinput.Model
	instanceForm   *huh.Form
	chosenInstance string
	help           help.Model
	width          int
	height         int
}

func NewOverview(ctx context.Context, client *clients.Client) *Overview {
	filter := textinput.New()
	filter.Prompt = "filter: "
	filter.Placeholder = "name or address"
	loadCtx, cancelLoad := context.WithCancel(ctx)
	return &Overview{
		ctx:        ctx,
		loadCtx:    loadCtx,
		cancelLoad: cancelLoad,
		client:     client,
		backends:   make(map[string]*BackendInfo),
		expanded:   make(map[string]bool),
		filter:     filter,
		help:       help.New(),
	}
}

func (m *Overview) Init() tea.Cmd {
	return loadInstances(m.ctx, m.client)
}

// CapturesInput tells the app that q is typed text, not a quit request
func (m *Overview) CapturesInput() bool {
	return m.filter.Focused() || m.instanceForm != nil
}

func (m *Overview) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.help.SetWidth(msg.Width)
	case instancesLoadedMsg:
		return m.onInstancesLoaded(msg)
	case instanceLoadedMsg:
		m.onInstanceLoaded(msg)
		return nil
	case backendsLoadedMsg:
		for _, info := range msg.backends {
			m.backends[info.GetUUID()] = info
		}
		return nil
	case tuicommon.ReloadMsg:
		return m.reload()
	case tea.KeyPressMsg:
		return m.onKey(msg)
	}
	// the form also needs the messages which are not keys, like its cursor blink
	if m.instanceForm != nil {
		return m.updateInstanceForm(msg)
	}
	return nil
}

func (m *Overview) onInstancesLoaded(msg instancesLoadedMsg) tea.Cmd {
	if msg.err != nil {
		tui.Error(tuicommon.RpcErrorFormat(msg.err))
		return tea.Quit
	}
	m.instancesLoaded = true
	m.instances = msg.instances
	if len(m.instances) == 0 {
		return nil
	}
	m.inst = m.instances[0]
	return m.reload()
}

func (m *Overview) onInstanceLoaded(msg instanceLoadedMsg) {
	// the answer of an instance which is no longer shown
	if msg.instanceUuid != m.inst.GetUUID() {
		return
	}
	m.loading = false
	m.updatedAt = time.Now()
	m.loadErr = msg.err
	if msg.err == nil {
		m.data = msg.data
	}
	m.moveCursor(0)
}

func (m *Overview) reload() tea.Cmd {
	if m.inst == nil {
		return nil
	}
	m.cancelLoad()
	m.loadCtx, m.cancelLoad = context.WithCancel(m.ctx)
	m.loading = true
	backendUuids := make([]string, 0)
	for _, vsInfo := range m.visibleVss() {
		if m.expanded[vsInfo.GetUUID()] {
			backendUuids = append(backendUuids, vsInfo.GetBackends()...)
		}
	}
	m.backends = make(map[string]*BackendInfo)
	return tea.Batch(loadInstance(m.loadCtx, m.client, m.inst), loadBackends(m.loadCtx, m.client, backendUuids))
}

func (m *Overview) onKey(msg tea.KeyPressMsg) tea.Cmd {
	if m.instanceForm != nil {
		if msg.String() == "esc" {
			m.instanceForm = nil
			return nil
		}
		return m.updateInstanceForm(msg)
	}
	if m.filter.Focused() {
		return m.updateFilter(msg)
	}
	switch {
	case key.Matches(msg, keys.Up):
		m.moveCursor(-1)
	case key.Matches(msg, keys.Down):
		m.moveCursor(1)
	case key.Matches(msg, keys.PageUp):
		m.moveCursor(-pageMove)
	case key.Matches(msg, keys.PageDown):
		m.moveCursor(pageMove)
	case key.Matches(msg, keys.Expand):
		return m.toggleExpanded()
	case key.Matches(msg, keys.Filter):
		return m.filter.Focus()
	case key.Matches(msg, keys.ClearFilter):
		m.filter.SetValue("")
		m.cursor = 0
	case key.Matches(msg, keys.Instance):
		return m.openInstanceForm()
	case key.Matches(msg, keys.Reload):
		return m.reload()
	case key.Matches(msg, keys.Help):
		m.help.ShowAll = !m.help.ShowAll
	}
	return nil
}

func (m *Overview) moveCursor(delta int) {
	last := len(m.visibleVss()) - 1
	m.cursor = max(0, min(m.cursor+delta, last))
}

// updateFilter types in the filter until enter or esc, the filter stays applied after
func (m *Overview) updateFilter(msg tea.KeyPressMsg) tea.Cmd {
	if msg.String() == "esc" || msg.String() == "enter" {
		m.filter.Blur()
		return nil
	}
	var cmd tea.Cmd
	m.filter, cmd = m.filter.Update(msg)
	m.cursor = 0
	return cmd
}

func (m *Overview) visibleVss() []*VsInfo {
	if m.data == nil {
		return nil
	}
	query := strings.ToLower(strings.TrimSpace(m.filter.Value()))
	if query == "" {
		return m.data.vss
	}
	matching := make([]*VsInfo, 0)
	for _, vsInfo := range m.data.vss {
		searched := strings.ToLower(vsInfo.GetName() + " " + strings.Join(vsInfo.Addresses(), " "))
		if strings.Contains(searched, query) {
			matching = append(matching, vsInfo)
		}
	}
	return matching
}

func (m *Overview) selectedVs() *VsInfo {
	vss := m.visibleVss()
	if m.cursor >= len(vss) {
		return nil
	}
	return vss[m.cursor]
}

func (m *Overview) toggleExpanded() tea.Cmd {
	vsInfo := m.selectedVs()
	if vsInfo == nil {
		return nil
	}
	if m.expanded[vsInfo.GetUUID()] {
		delete(m.expanded, vsInfo.GetUUID())
		return nil
	}
	m.expanded[vsInfo.GetUUID()] = true
	unread := make([]string, 0)
	for _, backendUuid := range vsInfo.GetBackends() {
		if _, read := m.backends[backendUuid]; !read {
			unread = append(unread, backendUuid)
		}
	}
	return loadBackends(m.loadCtx, m.client, unread)
}

func (m *Overview) openInstanceForm() tea.Cmd {
	if len(m.instances) == 0 {
		return nil
	}
	options := make([]huh.Option[string], 0, len(m.instances))
	for _, inst := range m.instances {
		options = append(options, huh.NewOption(inst.GetName(), inst.GetUUID()))
	}
	m.chosenInstance = m.inst.GetUUID()
	m.instanceForm = huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().
			Title("Instance to look at").
			Description("enter to choose, esc to cancel").
			Options(options...).
			Value(&m.chosenInstance),
	)).WithTheme(tuicommon.FormTheme)
	return m.instanceForm.Init()
}

func (m *Overview) updateInstanceForm(msg tea.Msg) tea.Cmd {
	model, cmd := m.instanceForm.Update(msg)
	if form, ok := model.(*huh.Form); ok {
		m.instanceForm = form
	}
	if m.instanceForm.State != huh.StateCompleted {
		return cmd
	}
	m.instanceForm = nil
	for _, inst := range m.instances {
		if inst.GetUUID() == m.chosenInstance && inst.GetUUID() != m.inst.GetUUID() {
			m.inst = inst
			m.data = nil
			m.loadErr = nil
			m.expanded = make(map[string]bool)
			m.cursor = 0
			return m.reload()
		}
	}
	return nil
}

func (m *Overview) View() string {
	if m.instanceForm != nil {
		return lipgloss.JoinVertical(lipgloss.Left, m.viewTitle(), "", m.instanceForm.View())
	}
	head := []string{m.viewTitle()}
	if !m.instancesLoaded {
		return m.fitWidth(append(head, "", dimStyle.Render("loading instances…")))
	}
	if m.inst == nil {
		return m.fitWidth(append(head, "", "No instance found, an instance is created when a VIP needs agents."))
	}

	if m.data != nil {
		head = append(head, agentsLine(m.data.agents))
	}
	if m.loadErr != nil {
		head = append(head, errorStyle.Render("could not load the instance: ")+tuicommon.RpcErrorFormat(m.loadErr))
	}
	if m.filter.Focused() || m.filter.Value() != "" {
		head = append(head, m.filter.View())
	}
	head = append(head, "")

	vss := m.visibleVss()
	layout := newTableLayout(vss)
	head = append(head, layout.header(), layout.rule(m.width))
	footer := []string{"", m.help.View(keys)}

	body, selectedStart, selectedEnd := m.viewBody(vss, layout)
	bodyHeight := m.height - lipgloss.Height(strings.Join(head, "\n")) - lipgloss.Height(strings.Join(footer, "\n"))
	body = m.scrollBody(body, selectedStart, selectedEnd, bodyHeight)

	lines := append(append(head, body...), footer...)
	return m.fitWidth(lines)
}

func (m *Overview) viewTitle() string {
	left := boldStyle.Render("flock") + dimStyle.Render(" › ") + "overview"
	if m.inst != nil {
		left += dimStyle.Render("  instance ") + instanceStyle.Render(m.inst.GetName())
		if len(m.instances) > 1 {
			left += dimStyle.Render(" (/ to switch)")
		}
	}
	right := ""
	if m.loading {
		right = dimStyle.Render("loading…")
	} else if !m.updatedAt.IsZero() {
		right = dimStyle.Render("updated ") + timeStyle.Render(m.updatedAt.Format(time.TimeOnly))
	}
	gap := max(2, m.width-lipgloss.Width(left)-lipgloss.Width(right))
	return left + strings.Repeat(" ", gap) + right
}

// viewBody gives the table lines with the line range of the selected virtual server, end excluded
func (m *Overview) viewBody(vss []*VsInfo, layout tableLayout) ([]string, int, int) {
	if m.data == nil {
		return []string{dimStyle.Render("  loading virtual servers…")}, 0, 0
	}
	if len(m.data.vss) == 0 {
		return []string{"  No virtual server is announced by this instance."}, 0, 0
	}
	if len(vss) == 0 {
		return []string{"  No virtual server matches the filter."}, 0, 0
	}
	lines := make([]string, 0)
	selectedStart, selectedEnd := 0, 0
	for i, vsInfo := range vss {
		selected := i == m.cursor
		expanded := m.expanded[vsInfo.GetUUID()]
		if selected {
			selectedStart = len(lines)
		}
		lines = append(lines, layout.rows(vsInfo, expanded, selected, vsHealth(vsInfo.GetBackends(), m.data.endpoints))...)
		if expanded {
			lines = append(lines, backendLines(vsInfo, m.backends, m.data.endpoints)...)
		}
		if selected {
			selectedEnd = len(lines)
		}
	}
	return lines, selectedStart, selectedEnd
}

// scrollBody keeps the selected virtual server on screen, with as much of its expanded block as fits.
// The scroll position is kept between views so the list does not jump while moving.
func (m *Overview) scrollBody(lines []string, selectedStart, selectedEnd, height int) []string {
	if height <= 0 || len(lines) <= height {
		m.scroll = 0
		return lines
	}
	selectedEnd = min(selectedEnd, selectedStart+height)
	if selectedStart < m.scroll {
		m.scroll = selectedStart
	}
	if selectedEnd > m.scroll+height {
		m.scroll = selectedEnd - height
	}
	m.scroll = min(m.scroll, len(lines)-height)
	return lines[m.scroll : m.scroll+height]
}

// fitWidth cuts the lines longer than the terminal, a wrapped line would push the table off screen
func (m *Overview) fitWidth(lines []string) string {
	content := strings.Join(lines, "\n")
	if m.width <= 0 {
		return content
	}
	split := strings.Split(content, "\n")
	for i, line := range split {
		split[i] = ansi.Truncate(line, m.width, "…")
	}
	return strings.Join(split, "\n")
}
