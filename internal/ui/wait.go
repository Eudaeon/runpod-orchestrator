// Package ui renders the orchestrator's terminal UI with Bubble Tea.
package ui

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"runpod-orchestrator/internal/runpod"
)

// WaitInfo is the header information shown while waiting for the pod.
type WaitInfo struct {
	Workload string // shown in the header title, e.g. "hashcat" or "sage"
	Balance  float64
	GPUName  string
	Cores    int
	MemGB    int
	Price    float64
	PodID    string
}

// LogFetcher fetches a pod's logs (satisfied by *runpod.Client).
type LogFetcher interface {
	PodLogs(ctx context.Context, podID string) (*runpod.PodLogs, error)
}

const (
	headerHeight = 7
	footerHeight = 2
	pollInterval = 1500 * time.Millisecond
)

var (
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("63"))
	labelStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	valueStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	moneyStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("42"))
	sectStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	dimStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	hintStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
)

type tickMsg time.Time

type pollMsg struct {
	container []string
	system    []string
	ready     bool
}

type model struct {
	info     WaitInfo
	client   LogFetcher
	bindPort int
	deadline time.Time

	spinner  spinner.Model
	viewport viewport.Model
	ready    bool
	vpReady  bool

	container []string
	system    []string

	aborted bool
	timeout bool
}

func newModel(client LogFetcher, bindPort int, info WaitInfo, timeout time.Duration) model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("63"))
	return model{
		info:     info,
		client:   client,
		bindPort: bindPort,
		deadline: time.Now().Add(timeout),
		spinner:  s,
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.pollCmd())
}

// pollCmd checks the bind port and refreshes the pod logs.
func (m model) pollCmd() tea.Cmd {
	bindPort, client, podID := m.bindPort, m.client, m.info.PodID
	return func() tea.Msg {
		msg := pollMsg{}
		if c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", bindPort), 2*time.Second); err == nil {
			c.Close()
			msg.ready = true
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if logs, err := client.PodLogs(ctx, podID); err == nil {
			msg.container, msg.system = logs.Container, logs.System
		}
		return msg
	}
}

func tickCmd() tea.Cmd {
	return tea.Tick(pollInterval, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q", "esc":
			m.aborted = true
			return m, tea.Quit
		}

	case tea.WindowSizeMsg:
		vpHeight := msg.Height - headerHeight - footerHeight
		if vpHeight < 3 {
			vpHeight = 3
		}
		if !m.vpReady {
			m.viewport = viewport.New(msg.Width, vpHeight)
			m.vpReady = true
		} else {
			m.viewport.Width = msg.Width
			m.viewport.Height = vpHeight
		}
		m.viewport.SetContent(m.logContent())
		m.viewport.GotoBottom()
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case pollMsg:
		m.container, m.system = msg.container, msg.system
		m.ready = msg.ready
		if m.vpReady {
			atBottom := m.viewport.AtBottom()
			m.viewport.SetContent(m.logContent())
			if atBottom {
				m.viewport.GotoBottom()
			}
		}
		if m.ready {
			return m, tea.Quit
		}
		if time.Now().After(m.deadline) {
			m.timeout = true
			return m, tea.Quit
		}
		return m, tickCmd()

	case tickMsg:
		return m, m.pollCmd()
	}

	// Forward remaining messages (scroll keys etc.) to the viewport.
	if m.vpReady {
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m model) logContent() string {
	var b strings.Builder
	b.WriteString(sectStyle.Render("System logs"))
	b.WriteByte('\n')
	if len(m.system) == 0 {
		b.WriteString(dimStyle.Render("  (waiting...)") + "\n")
	}
	for _, l := range m.system {
		b.WriteString("  " + stripTimestamp(l) + "\n")
	}
	b.WriteByte('\n')
	b.WriteString(sectStyle.Render("Container logs"))
	b.WriteByte('\n')
	if len(m.container) == 0 {
		b.WriteString(dimStyle.Render("  (waiting for container to start...)") + "\n")
	}
	for _, l := range m.container {
		b.WriteString("  " + stripTimestamp(l) + "\n")
	}
	return b.String()
}

func (m model) View() string {
	header := m.headerView()
	footer := m.footerView()
	body := ""
	if m.vpReady {
		body = m.viewport.View()
	}
	return header + "\n" + body + "\n" + footer
}

func (m model) headerView() string {
	workload := m.info.Workload
	if workload == "" {
		workload = "hashcat"
	}
	hardware := fmt.Sprintf("%s (%d vCPU, %d GB)", m.info.GPUName, m.info.Cores, m.info.MemGB)
	lines := []string{
		titleStyle.Render("RunPod orchestrator — " + workload),
		labelStyle.Render("Balance  ") + moneyStyle.Render(fmt.Sprintf("$%.2f", m.info.Balance)),
		labelStyle.Render("Hardware ") + valueStyle.Render(hardware),
		labelStyle.Render("Price    ") + valueStyle.Render(fmt.Sprintf("$%.2f/hr", m.info.Price)),
		labelStyle.Render("Pod      ") + valueStyle.Render(m.info.PodID),
		dimStyle.Render(strings.Repeat("─", 50)),
	}
	return strings.Join(lines, "\n")
}

func (m model) footerView() string {
	status := m.spinner.View() + " Waiting for the pod to come online..."
	hint := hintStyle.Render("press q to abort")
	return status + "\n" + hint
}

// RunWait shows the waiting UI until the pod's shell is reachable on bindPort.
// It returns ready=true once reachable, or ready=false if the user aborted.
// A non-nil error indicates the pod did not come online in time.
func RunWait(ctx context.Context, client LogFetcher, bindPort int, info WaitInfo, timeout time.Duration) (ready bool, err error) {
	m := newModel(client, bindPort, info, timeout)
	p := tea.NewProgram(m, tea.WithContext(ctx), tea.WithAltScreen())
	final, runErr := p.Run()
	if runErr != nil {
		// Context cancellation (Ctrl-C during wait) surfaces here.
		if ctx.Err() != nil {
			return false, nil
		}
		return false, runErr
	}
	fm := final.(model)
	switch {
	case fm.aborted:
		return false, nil
	case fm.timeout:
		return false, fmt.Errorf("pod did not come online within %s", timeout)
	default:
		return fm.ready, nil
	}
}

// stripTimestamp removes a leading RFC3339-ish timestamp token from a log line.
func stripTimestamp(line string) string {
	if i := strings.IndexByte(line, ' '); i > 0 && i < 35 && strings.ContainsAny(line[:i], "TZ:") {
		return line[i+1:]
	}
	return line
}
