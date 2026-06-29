package ui

import (
	"context"
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"runpod-orchestrator/internal/runpod"
)

var (
	pickTitleStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("63"))
	pickHeaderStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("244"))
	pickCursorStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("231")).Background(lipgloss.Color("63"))
	pickRowStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	pickDimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	pickHelpStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	pickNoticeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	pickErrStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	pickBigStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("231"))
	pickBoxStyle    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("63"))
)

// GpuCountFetcher resolves the offering for `count` of a GPU (vCPU/RAM/price all
// scale with the count), returning nil if RunPod can't place that count right
// now. It is run off the UI goroutine via a tea.Cmd.
type GpuCountFetcher func(gpuID string, count int) (*runpod.GpuType, error)

type pickPhase int

const (
	phaseGPU pickPhase = iota
	phaseCount
)

// countsMsg carries the per-count offerings discovered for the chosen GPU.
type countsMsg struct {
	byCount map[int]*runpod.GpuType
	err     error
}

// gpuPicker is a two-step chooser: an interactive table of Secure Cloud GPUs,
// then a GPU-count selector (1 → the GPU's secure-cloud max) that shows the
// resulting vCPU / RAM / price. Unavailable rows/counts are skipped.
type gpuPicker struct {
	gpus  []runpod.GpuType
	fetch GpuCountFetcher

	phase  pickPhase
	cursor int
	offset int

	// Count phase, for the GPU at cursor.
	countMin int
	countMax int
	count    int
	byCount  map[int]*runpod.GpuType
	loading  bool
	countErr string

	// pinnedCount, when > 0, is a --count supplied on the command line: the count
	// phase is skipped and this many GPUs are deployed when the chosen GPU can be
	// placed at that count, falling back to interactive selection otherwise.
	pinnedCount int

	result *runpod.GpuType // confirmed offering (priced for `count0` GPUs)
	count0 int
	err    error // hard fetch error to surface to the caller

	notice string
	nameW  int
	width  int
	height int
}

func newGpuPicker(gpus []runpod.GpuType, notice, preferredID string, pinnedCount int, jumpToCount bool, fetch GpuCountFetcher) gpuPicker {
	// Sort by cost-efficiency, most Hash/$ first; GPUs without a benchmark (or a
	// known price) keep their incoming order at the end. Copy first so the
	// caller's slice is left untouched.
	gpus = append([]runpod.GpuType(nil), gpus...)
	sort.SliceStable(gpus, func(i, j int) bool {
		hi, oki := gpuHashPerDollar(gpus[i])
		hj, okj := gpuHashPerDollar(gpus[j])
		if oki != okj {
			return oki // benchmarked rows ahead of un-benchmarked ones
		}
		return hi > hj
	})

	nameW := len("NAME")
	for _, g := range gpus {
		if len(g.ID) > nameW {
			nameW = len(g.ID)
		}
	}
	m := gpuPicker{gpus: gpus, fetch: fetch, notice: notice, nameW: nameW, pinnedCount: pinnedCount}
	// Start on the requested GPU when it's available, else the first available
	// row, else the top of the table.
	m.cursor = -1
	for i := range gpus {
		if gpus[i].ID == preferredID && pickerAvailable(gpus[i]) {
			m.cursor = i
			break
		}
	}
	if m.cursor < 0 {
		for i := range gpus {
			if pickerAvailable(gpus[i]) {
				m.cursor = i
				break
			}
		}
	}
	if m.cursor < 0 {
		m.cursor = 0
	}

	// With a pinned --gpu (jumpToCount) that landed on its available row, open
	// directly on that GPU's count phase. Init() kicks off the per-count load.
	// Otherwise fall back to the GPU table so the user can pick another.
	if jumpToCount && len(m.gpus) > 0 && m.gpus[m.cursor].ID == preferredID && pickerAvailable(m.gpus[m.cursor]) {
		m.enterCount(m.gpus[m.cursor])
	}
	return m
}

// enterCount switches to the count phase for g and marks it loading; the caller
// (Init or the GPU-phase enter handler) issues the matching countsCmd.
func (m *gpuPicker) enterCount(g runpod.GpuType) {
	m.phase = phaseCount
	m.loading = true
	m.byCount, m.countErr, m.count = nil, "", 0
	m.countMin = g.MinPodGpuCount
	if m.countMin < 1 {
		m.countMin = 1
	}
	m.countMax = g.MaxGpuCountSecure
	if m.countMax < m.countMin {
		m.countMax = m.countMin
	}
}

// pickerAvailable reports whether a GPU has live single-GPU stock to deploy.
func pickerAvailable(g runpod.GpuType) bool {
	return g.OnDemandPrice > 0 && g.StockStatus != ""
}

func (m gpuPicker) Init() tea.Cmd {
	// When opened directly on the count phase (pinned --gpu), load its offerings.
	if m.phase == phaseCount && m.loading {
		g := m.gpus[m.cursor]
		return m.countsCmd(g.ID, m.countMin, m.countMax)
	}
	return nil
}

func (m gpuPicker) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.scroll()
		return m, nil

	case countsMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err // surface hard errors to the caller
			return m, tea.Quit
		}
		m.byCount = msg.byCount
		// A pinned --count skips the count phase entirely when that count can be
		// placed for the chosen GPU; otherwise fall through to interactive choice.
		if m.pinnedCount > 0 {
			if o := m.byCount[m.pinnedCount]; o != nil {
				m.result, m.count0 = o, m.pinnedCount
				return m, tea.Quit
			}
		}
		// Seed the selection to the lowest deployable count.
		m.count = 0
		for c := m.countMin; c <= m.countMax; c++ {
			if msg.byCount[c] != nil {
				m.count = c
				break
			}
		}
		if m.count == 0 {
			m.countErr = "No deployable GPU count right now — press esc to pick another."
		}
		return m, nil

	case tea.KeyMsg:
		if m.phase == phaseCount {
			return m.updateCount(msg)
		}
		return m.updateGPU(msg)
	}
	return m, nil
}

func (m gpuPicker) updateGPU(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q", "esc":
		return m, tea.Quit
	case "up", "k":
		m.move(-1)
	case "down", "j":
		m.move(1)
	case "enter":
		if len(m.gpus) > 0 && pickerAvailable(m.gpus[m.cursor]) {
			g := m.gpus[m.cursor]
			m.enterCount(g)
			return m, m.countsCmd(g.ID, m.countMin, m.countMax)
		}
	}
	return m, nil
}

func (m gpuPicker) updateCount(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "esc", "backspace":
		m.phase = phaseGPU
		m.countErr = ""
		return m, nil
	}
	if m.loading || m.count == 0 {
		return m, nil
	}
	switch msg.String() {
	case "left", "h":
		m.stepCount(-1)
	case "right", "l":
		m.stepCount(1)
	case "enter":
		if g := m.byCount[m.count]; g != nil {
			m.result, m.count0 = g, m.count
			return m, tea.Quit
		}
	}
	return m, nil
}

// stepCount moves the selected count by delta, skipping counts RunPod can't
// currently place.
func (m *gpuPicker) stepCount(delta int) {
	for c := m.count + delta; c >= m.countMin && c <= m.countMax; c += delta {
		if m.byCount[c] != nil {
			m.count = c
			return
		}
	}
}

func (m gpuPicker) countsCmd(id string, lo, hi int) tea.Cmd {
	f := m.fetch
	return func() tea.Msg {
		by := make(map[int]*runpod.GpuType, hi-lo+1)
		for c := lo; c <= hi; c++ {
			g, err := f(id, c)
			if err != nil {
				return countsMsg{err: err}
			}
			by[c] = g
		}
		return countsMsg{byCount: by}
	}
}

// move steps the cursor by delta, skipping unavailable rows and stopping at the
// last available row rather than walking off either end.
func (m *gpuPicker) move(delta int) {
	for i := m.cursor + delta; i >= 0 && i < len(m.gpus); i += delta {
		if pickerAvailable(m.gpus[i]) {
			m.cursor = i
			m.scroll()
			return
		}
	}
}

// visibleRows is how many table rows fit given the terminal height and chrome.
func (m gpuPicker) visibleRows() int {
	if m.height <= 0 {
		if len(m.gpus) < 18 {
			return len(m.gpus)
		}
		return 18
	}
	reserve := 6 // border(2) + title + header + help + one blank safety
	if m.notice != "" {
		reserve += 2
	}
	v := m.height - reserve
	if v < 3 {
		v = 3
	}
	if v > len(m.gpus) {
		v = len(m.gpus)
	}
	return v
}

// scroll keeps the cursor within the visible window (sticky, not re-centering).
func (m *gpuPicker) scroll() {
	v := m.visibleRows()
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+v {
		m.offset = m.cursor - v + 1
	}
	if max := len(m.gpus) - v; m.offset > max {
		m.offset = max
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

// row formats one table line to a fixed width so the box border stays aligned. A
// leading and trailing space keep text off the border, and the 2-column cursor
// marker is part of the width so the highlight bar spans the full row.
func (m gpuPicker) row(marker, name, price, speed, hash, cores, avail string) string {
	return fmt.Sprintf(" %-2s%-*s  %8s  %11s  %10s  %5s  %-11s ", marker, m.nameW, name, price, speed, hash, cores, avail)
}

// benchCells returns the SPEED and HASH/$ cell text for a GPU. Speed is the
// stored MD4 benchmark in GH/s; Hash/$ is derived from it and the GPU's live
// listed price (TH per dollar = GH/s × 3600 ÷ price ÷ 1000). Both are "—" when
// the GPU has no benchmark, and Hash/$ alone is "—" when no price is known.
func benchCells(g runpod.GpuType) (speed, hash string) {
	ghs, ok := gpuBenchGHs[g.ID]
	if !ok {
		return "—", "—"
	}
	speed, hash = fmt.Sprintf("%.2f GH/s", ghs), "—"
	if hpd, ok := gpuHashPerDollar(g); ok {
		hash = fmt.Sprintf("%.1f TH/$", hpd)
	}
	return speed, hash
}

func (m gpuPicker) View() string {
	if m.phase == phaseCount {
		return m.viewCount()
	}
	return m.viewGPU()
}

func (m gpuPicker) viewGPU() string {
	var b strings.Builder
	b.WriteString(pickTitleStyle.Render(m.row("", "Select a Secure Cloud GPU", "", "", "", "", "")))
	b.WriteByte('\n')
	b.WriteString(pickHeaderStyle.Render(m.row("", "NAME", "PRICE/HR", "SPEED", "HASH/$", "CORES", "AVAIL")))

	v := m.visibleRows()
	for i := m.offset; i < m.offset+v && i < len(m.gpus); i++ {
		g := m.gpus[i]
		price, cores, avail := "—", "—", "unavailable"
		if g.SecurePrice > 0 {
			price = fmt.Sprintf("$%.2f", g.SecurePrice)
		}
		if g.MinVcpu > 0 {
			cores = fmt.Sprintf("%d", g.MinVcpu)
		}
		if g.StockStatus != "" {
			avail = g.StockStatus
		}
		speed, hash := benchCells(g)
		marker := ""
		if i == m.cursor {
			marker = "▸"
		}
		line := m.row(marker, g.ID, price, speed, hash, cores, avail)
		b.WriteByte('\n')
		switch {
		case i == m.cursor:
			b.WriteString(pickCursorStyle.Render(line))
		case pickerAvailable(g):
			b.WriteString(pickRowStyle.Render(line))
		default:
			b.WriteString(pickDimStyle.Render(line))
		}
	}

	box := pickBoxStyle.Render(b.String())
	help := "↑/↓ move · enter select · q quit"
	if v < len(m.gpus) {
		help = fmt.Sprintf("%s · %d/%d", help, m.cursor+1, len(m.gpus))
	}
	out := box + "\n" + pickHelpStyle.Render(help)
	if m.notice != "" {
		out = pickNoticeStyle.Render(m.notice) + "\n\n" + out
	}
	return out
}

func (m gpuPicker) viewCount() string {
	g := m.gpus[m.cursor]
	w := len(m.row("", "", "", "", "", "", "")) // inner width, matches the GPU table

	title := pickTitleStyle.Render(center("GPUs · "+g.ID, w))

	var mid, sub, foot string
	switch {
	case m.loading:
		mid = center(pickDimStyle.Render("loading configurations…"), w)
		sub, foot = center("", w), center("", w)
	case m.count == 0:
		mid = center(pickErrStyle.Render(m.countErr), w)
		sub, foot = center("", w), center("", w)
	default:
		unit := "GPU"
		if m.count != 1 {
			unit = "GPUs"
		}
		mid = center(fmt.Sprintf("◂   %s   ▸", pickBigStyle.Render(fmt.Sprintf("%d %s", m.count, unit))), w)
		o := m.byCount[m.count]
		per := o.OnDemandPrice / float64(m.count)
		sub = center(pickRowStyle.Render(fmt.Sprintf("%d vCPU · %d GB RAM · $%.2f/hr", o.MinVcpu, o.MinMemoryInGb, o.OnDemandPrice)), w)
		foot = center(pickDimStyle.Render(fmt.Sprintf("%d × $%.2f · range %d–%d · enter to launch", m.count, per, m.countMin, m.countMax)), w)
	}

	body := strings.Join([]string{title, "", mid, "", sub, foot}, "\n")
	box := pickBoxStyle.Render(body)
	help := pickHelpStyle.Render("←/→ adjust · enter launch · esc back · q quit")
	return box + "\n" + help
}

// center pads s to display width w, centered (ANSI styling in s is not counted).
func center(s string, w int) string {
	pad := w - lipgloss.Width(s)
	if pad <= 0 {
		return s
	}
	left := pad / 2
	return strings.Repeat(" ", left) + s + strings.Repeat(" ", pad-left)
}

// PickGpu shows the interactive GPU + count chooser and returns the resolved
// offering (priced for the chosen count) and that count, or a nil GPU if the
// user quit. notice, when non-empty, is shown above the table. preferredID
// pre-selects that GPU's row when it's available. When pinnedCount > 0 the count
// phase is skipped and that count is used for the chosen GPU when placeable;
// otherwise the count selector starts at the lowest deployable count. When
// jumpToCount is set the picker opens directly on the count phase for the
// preferred GPU. fetch resolves a GPU at a given count against RunPod.
func PickGpu(ctx context.Context, gpus []runpod.GpuType, notice, preferredID string, pinnedCount int, jumpToCount bool, fetch GpuCountFetcher) (*runpod.GpuType, int, error) {
	p := tea.NewProgram(newGpuPicker(gpus, notice, preferredID, pinnedCount, jumpToCount, fetch), tea.WithContext(ctx), tea.WithAltScreen())
	final, err := p.Run()
	if err != nil {
		if ctx.Err() != nil {
			return nil, 0, nil
		}
		return nil, 0, err
	}
	fm := final.(gpuPicker)
	if fm.err != nil {
		return nil, 0, fm.err
	}
	return fm.result, fm.count0, nil
}
