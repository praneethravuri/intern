package ui

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	lip "github.com/charmbracelet/lipgloss"
	"github.com/praneethravuri/helios/pkg/protocol"
)

type mode int

const (
	modeList mode = iota
	modeInput
	modeSending
)

type Model struct {
	sessions []string
	cursor   int
	err      error

	width int

	mode       mode
	input      textinput.Model
	spinner    spinner.Model
	lastResult string
	lastErr    error
}

type SessionListTick struct{}

type sessionsMsg struct {
	sessions []string
	err      error
}

type broadcastResultMsg struct {
	raw string
	err error
}

func New() Model {
	ti := textinput.New()
	ti.Placeholder = "message to broadcast"
	ti.CharLimit = 512

	sp := spinner.New()
	sp.Spinner = spinner.Dot

	return Model{input: ti, spinner: sp}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(fetchSessions, tick())
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		return m, nil

	case tea.KeyMsg:
		switch m.mode {
		case modeList:
			return m.updateList(msg)
		case modeInput:
			return m.updateInput(msg)
		case modeSending:
			if msg.String() == "ctrl+c" {
				return m, tea.Quit
			}
			return m, nil
		}

	case SessionListTick:
		return m, tea.Batch(fetchSessions, tick())

	case sessionsMsg:
		m.sessions, m.err = msg.sessions, msg.err
		if m.cursor > len(m.sessions) {
			m.cursor = len(m.sessions)
		}
		return m, nil

	case broadcastResultMsg:
		m.mode = modeList
		if msg.err != nil {
			m.lastErr = msg.err
			m.lastResult = ""
		} else {
			m.lastErr = nil
			m.lastResult = strings.TrimSpace(msg.raw)
		}
		return m, nil

	case spinner.TickMsg:
		if m.mode == modeSending {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
		return m, nil
	}

	return m, nil
}

func (m Model) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.sessions) {
			m.cursor++
		}
	case "enter":
		m.mode = modeInput
		m.input.SetValue("")
		return m, tea.Batch(m.input.Focus(), textinput.Blink)
	}
	return m, nil
}

func (m Model) updateInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.mode = modeList
		m.input.Blur()
		return m, nil
	case "enter":
		target := broadcastTarget(m.sessions, m.cursor)
		message := m.input.Value()
		m.input.Blur()
		m.mode = modeSending
		return m, tea.Batch(m.spinner.Tick, sendBroadcast(target, message))
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// fetchSessions asks heliosd for the active session list over its Unix socket.
func fetchSessions() tea.Msg {
	conn, err := net.Dial("unix", protocol.SocketPath)
	if err != nil {
		return sessionsMsg{err: err}
	}
	defer conn.Close()

	if _, err := conn.Write([]byte(protocol.VerbList + "\n")); err != nil {
		return sessionsMsg{err: err}
	}

	var ids []string
	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		if line := scanner.Text(); line != "" && line != "No active sessions found." {
			ids = append(ids, line)
		}
	}
	if err := scanner.Err(); err != nil {
		return sessionsMsg{err: err}
	}

	return sessionsMsg{sessions: ids}
}

// sendBroadcast dials the daemon, sends a BROADCAST for target/message, and
// returns its reply as a broadcastResultMsg.
func sendBroadcast(target, message string) tea.Cmd {
	return func() tea.Msg {
		handshake, err := protocol.FormatBroadcast(target, message)
		if err != nil {
			return broadcastResultMsg{err: err}
		}

		conn, err := net.Dial("unix", protocol.SocketPath)
		if err != nil {
			return broadcastResultMsg{err: err}
		}
		defer conn.Close()

		if _, err := conn.Write([]byte(handshake)); err != nil {
			return broadcastResultMsg{err: err}
		}

		buf := make([]byte, 4096)
		n, err := conn.Read(buf)
		if err != nil {
			return broadcastResultMsg{err: err}
		}
		return broadcastResultMsg{raw: string(buf[:n])}
	}
}

func (m Model) View() tea.View {
	left, right := paneWidths(m.width)

	leftPane := lip.JoinVertical(lip.Top,
		m.renderSessionsPane(left),
		m.renderBroadcastPane(left),
		m.renderStatusPane(left),
	)

	rightPane := m.renderHelpPane(right)

	view := lip.JoinHorizontal(lip.Top, leftPane, rightPane)
	v := tea.NewView(view)
	v.AltScreen = true
	return v
}

func paneStyle(width int) lip.Style {
	return lip.NewStyle().Border(lip.RoundedBorder()).Padding(1).Width(width)
}

// paneWidths splits the terminal width into a left (sessions/broadcast/status)
// column and a right (help) column. Falls back to the pre-resize defaults
// (30/40) when total is unknown or too small to lay out sensibly.
func paneWidths(total int) (left, right int) {
	const minLeft, minRight = 30, 30
	if total < minLeft+minRight {
		return minLeft, 40
	}
	left = total * 45 / 100
	if left < minLeft {
		left = minLeft
	}
	right = total - left
	if right < minRight {
		right = minRight
	}
	return left, right
}

// broadcastTarget maps a session-list cursor position to a BROADCAST target:
// cursor 0 is the synthetic "all sessions" row, cursor N>=1 is sessions[N-1].
func broadcastTarget(sessions []string, cursor int) string {
	if cursor <= 0 || cursor > len(sessions) {
		return protocol.BroadcastAll
	}
	return sessions[cursor-1]
}

// renderSessionList renders the "All sessions" row plus each session ID, with
// a ">" marker on the cursor row, truncated to maxRows with a "+N more" trailer.
func renderSessionList(sessions []string, cursor int, maxRows int) string {
	rows := make([]string, 0, len(sessions)+1)
	rows = append(rows, "All sessions")
	rows = append(rows, sessions...)

	shown := rows
	var trailer string
	if maxRows > 0 && len(rows) > maxRows {
		shown = rows[:maxRows]
		trailer = fmt.Sprintf("\n  +%d more", len(rows)-maxRows)
	}

	lines := make([]string, len(shown))
	for i, row := range shown {
		marker := "  "
		if i == cursor {
			marker = "> "
		}
		lines[i] = marker + row
	}
	return strings.Join(lines, "\n") + trailer
}

func (m Model) renderSessionsPane(width int) string {
	var body string
	switch {
	case m.err != nil:
		body = lip.NewStyle().Faint(true).Width(width - 4).Render("error: " + m.err.Error())
	default:
		body = renderSessionList(m.sessions, m.cursor, 8)
	}
	return paneStyle(width).Render("Sessions:\n" + body)
}

func (m Model) renderBroadcastPane(width int) string {
	target := broadcastTarget(m.sessions, m.cursor)
	title := fmt.Sprintf("Broadcast -> %s:", target)

	var body string
	switch m.mode {
	case modeInput, modeSending:
		body = m.input.View()
	default:
		body = lip.NewStyle().Faint(true).Render("enter: compose a broadcast")
	}
	return paneStyle(width).Render(title + "\n" + body)
}

func (m Model) renderStatusPane(width int) string {
	var body string
	switch {
	case m.mode == modeSending:
		body = m.spinner.View() + " sending..."
	case m.lastErr != nil:
		body = lip.NewStyle().Faint(true).Width(width - 4).Render("error: " + m.lastErr.Error())
	case m.lastResult != "":
		body = m.lastResult
	default:
		body = lip.NewStyle().Faint(true).Render("(no broadcast sent yet)")
	}
	return paneStyle(width).Render("Status:\n" + body)
}

func (m Model) renderHelpPane(width int) string {
	help := strings.Join([]string{
		"up/down (or j/k)  select session",
		"enter             compose / send",
		"esc               cancel compose",
		"q, ctrl+c         quit",
	}, "\n")
	return paneStyle(width).Render("Keys:\n" + lip.NewStyle().Faint(true).Render(help))
}

func tick() tea.Cmd {
	return tea.Tick(1*time.Second, func(time.Time) tea.Msg {
		return SessionListTick{}
	})
}
