package ui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"runpod-orchestrator/internal/runpod"
)

// CpuFetcher resolves a CPU offering at the given vCPU count, returning nil when
// RunPod can't place that size right now. It is run off the UI goroutine.
type CpuFetcher func(vcpu int) (*runpod.CpuInstance, error)

// cpuLoadedMsg carries the offering discovered for each candidate vCPU size.
type cpuLoadedMsg struct {
	byVcpu map[int]*runpod.CpuInstance
	err    error
}

// cpuPicker is a one-step horizontal chooser over a ladder of vCPU sizes for a
// fixed CPU flavor, showing the resulting RAM / price / stock. Unavailable sizes
// are skipped.
type cpuPicker struct {
	label   string
	notice  string
	options []int
	fetch   CpuFetcher

	byVcpu  map[int]*runpod.CpuInstance
	idx     int
	loading bool

	result *runpod.CpuInstance
	err    error
	width  int
}

func newCpuPicker(label, notice string, options []int, fetch CpuFetcher) cpuPicker {
	return cpuPicker{label: label, notice: notice, options: options, fetch: fetch, loading: true}
}

func (m cpuPicker) Init() tea.Cmd { return m.loadCmd() }

func (m cpuPicker) loadCmd() tea.Cmd {
	f, opts := m.fetch, m.options
	return func() tea.Msg {
		by := make(map[int]*runpod.CpuInstance, len(opts))
		for _, v := range opts {
			inst, err := f(v)
			if err != nil {
				return cpuLoadedMsg{err: err}
			}
			by[v] = inst
		}
		return cpuLoadedMsg{byVcpu: by}
	}
}

func (m cpuPicker) available(vcpu int) bool {
	inst := m.byVcpu[vcpu]
	return inst != nil && inst.Price > 0 && inst.StockStatus != ""
}

func (m cpuPicker) anyAvailable() bool {
	for _, v := range m.options {
		if m.available(v) {
			return true
		}
	}
	return false
}

func (m *cpuPicker) step(delta int) {
	for i := m.idx + delta; i >= 0 && i < len(m.options); i += delta {
		if m.available(m.options[i]) {
			m.idx = i
			return
		}
	}
}

func (m cpuPicker) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		return m, nil

	case cpuLoadedMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
			return m, tea.Quit
		}
		m.byVcpu = msg.byVcpu
		// Start on the lowest available size (options are ascending).
		m.idx = 0
		for i, v := range m.options {
			if m.available(v) {
				m.idx = i
				break
			}
		}
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q", "esc":
			return m, tea.Quit
		}
		if m.loading {
			return m, nil
		}
		switch msg.String() {
		case "left", "h":
			m.step(-1)
		case "right", "l":
			m.step(1)
		case "enter":
			if v := m.options[m.idx]; m.available(v) {
				m.result = m.byVcpu[v]
				return m, tea.Quit
			}
		}
	}
	return m, nil
}

func (m cpuPicker) View() string {
	const w = 46
	title := pickTitleStyle.Render(center(m.label, w))

	mid, sub, foot := center("", w), center("", w), center("", w)
	switch {
	case m.loading:
		mid = center(pickDimStyle.Render("loading configurations…"), w)
	case !m.anyAvailable():
		mid = center(pickErrStyle.Render("No deployable CPU size right now."), w)
	default:
		v := m.options[m.idx]
		inst := m.byVcpu[v]
		mid = center(fmt.Sprintf("◂   %s   ▸", pickBigStyle.Render(fmt.Sprintf("%d vCPU", v))), w)
		sub = center(pickRowStyle.Render(fmt.Sprintf("%d GB RAM · $%.2f/hr · %s", inst.RamGb, inst.Price, inst.StockStatus)), w)
		foot = center(pickDimStyle.Render("enter to launch"), w)
	}

	body := strings.Join([]string{title, "", mid, "", sub, foot}, "\n")
	out := pickBoxStyle.Render(body) + "\n" + pickHelpStyle.Render("←/→ adjust · enter launch · q quit")
	if m.notice != "" {
		out = pickNoticeStyle.Render(m.notice) + "\n\n" + out
	}
	return out
}

// PickCpu shows the interactive vCPU-size chooser for a fixed CPU flavor and
// returns the resolved offering, or nil if the user quit. label heads the box;
// notice, when non-empty, is shown above it. options is the ladder of vCPU
// sizes to offer; the picker starts on the lowest available one. fetch resolves
// each size against RunPod.
func PickCpu(ctx context.Context, label, notice string, options []int, fetch CpuFetcher) (*runpod.CpuInstance, error) {
	p := tea.NewProgram(newCpuPicker(label, notice, options, fetch), tea.WithContext(ctx), tea.WithAltScreen())
	final, err := p.Run()
	if err != nil {
		if ctx.Err() != nil {
			return nil, nil
		}
		return nil, err
	}
	fm := final.(cpuPicker)
	if fm.err != nil {
		return nil, fm.err
	}
	return fm.result, nil
}
