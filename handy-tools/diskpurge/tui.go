package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
	"unsafe"
)

const (
	reset  = "\x1b[0m"
	bold   = "\x1b[1m"
	white  = "\x1b[38;5;194m"
	green  = "\x1b[38;5;46m"
	mint   = "\x1b[38;5;121m"
	leaf   = "\x1b[38;5;34m"
	forest = "\x1b[38;5;28m"
	shade  = "\x1b[38;5;22m"
	amber  = "\x1b[38;5;214m"
	red    = "\x1b[38;5;203m"
	pink   = "\x1b[38;5;218m"
	// bg pins a near-black background under every cell, so the green palette
	// keeps its contrast on light terminal themes too.
	bg = "\x1b[48;5;233m"
)

const rainChars = "ｱｲｳｴｵｶｷｸｹｺｻｼｽｾｿﾀﾁﾂﾃﾄﾅﾆﾇﾈﾉﾊﾋﾌﾍﾎﾏﾐﾑﾒﾓﾔﾕﾖﾗﾘﾙﾚﾛﾜﾝ0123456789"

type sizeMsg struct {
	item *Item
	size int64
}

// runTUI owns the terminal: raw input, alternate screen, and the render loop.
// Keys, size scans and purge progress all arrive on channels. The purge runs
// on its own goroutine, so the screen keeps repainting while it works.
func runTUI(m *Model) error {
	saved, err := stty("-g")
	if err != nil {
		return fmt.Errorf("needs an interactive terminal")
	}
	if _, err := stty("raw", "-echo"); err != nil {
		return err
	}
	defer func() {
		_, _ = stty(strings.TrimSpace(saved))
		fmt.Print(reset + "\x1b[?25h\x1b[?1049l")
	}()
	fmt.Print("\x1b[?1049h\x1b[?25l")

	// Raw mode turns ctrl-c into a plain key, but a kill or a closed window
	// still arrives as a signal: leave through the deferred restore above.
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sigs)

	keys := make(chan string)
	go readKeys(keys)

	sizes := make(chan sizeMsg)
	for _, g := range m.Groups {
		for _, it := range g.Items {
			if it.Scanned {
				continue // measured while the catalog was built
			}
			go func(it *Item) {
				sizes <- sizeMsg{item: it, size: dirSize(it.Path)}
			}(it)
		}
	}

	ticker := time.NewTicker(80 * time.Millisecond)
	defer ticker.Stop()
	v := &view{}

	var (
		events = make(chan runMsg)
		input  = make(chan []byte)
		cancel chan struct{}
		doneAt time.Time
	)

	v.draw(m)
	for {
		// Command output can arrive in bursts, so it only updates the model;
		// the next tick (at most 80ms away) paints it.
		redraw := false
		select {
		case raw, ok := <-keys:
			if !ok {
				return nil
			}
			redraw = true
			switch m.Mode {
			case modeRunning:
				if strings.Contains(raw, "\x03") {
					if m.Stopped {
						return nil // second ctrl-c: leave right now
					}
					m.Stopped = true
					close(cancel)
				}
				// Everything typed goes to the running command, if one is
				// listening, so it can be answered or interrupted.
				select {
				case input <- []byte(raw):
					m.noteTyped(splitKeys(raw))
				default:
				}
			case modeDone:
				// Ignore keys typed just as the run ended, so a stray key
				// meant for a command doesn't close the summary unread.
				if time.Since(doneAt) > 500*time.Millisecond {
					return nil
				}
			default:
				for _, k := range splitKeys(raw) {
					if quit := handleKey(m, k); quit {
						return nil
					}
					if m.Mode == modeRunning {
						cancel = make(chan struct{})
						go runJobs(m.Begin(), m.DryRun, events, input, cancel)
						break
					}
				}
			}
		case s := <-sizes:
			if !s.item.Done {
				s.item.Size = s.size
			}
			s.item.Scanned = true
		case e := <-events:
			m.Apply(e)
			if m.Mode == modeDone {
				doneAt = time.Now()
				redraw = true
			}
		case <-sigs:
			return nil
		case <-ticker.C:
			v.frame++
			redraw = true
		}
		if redraw {
			v.draw(m)
		}
	}
}

func readKeys(out chan<- string) {
	buf := make([]byte, 64)
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil {
			close(out)
			return
		}
		out <- string(buf[:n])
	}
}

// splitKeys cuts one read from the terminal into single keys. Keys typed
// quickly (or held down) arrive glued together, and arrow keys are several
// bytes long.
func splitKeys(raw string) []string {
	var keys []string
	for i := 0; i < len(raw); {
		if raw[i] == 0x1b && i+1 < len(raw) && (raw[i+1] == '[' || raw[i+1] == 'O') {
			j := i + 2
			for j < len(raw) && (raw[j] < 0x40 || raw[j] > 0x7e) {
				j++
			}
			if j < len(raw) {
				j++
			}
			// Arrow keys come as ESC [ A or ESC O A depending on the mode.
			keys = append(keys, "\x1b["+raw[i+2:j])
			i = j
			continue
		}
		_, size := utf8.DecodeRuneInString(raw[i:])
		keys = append(keys, raw[i:i+size])
		i += size
	}
	return keys
}

// handleKey updates the model for one key in the browse or confirm screen
// and reports whether the program should exit.
func handleKey(m *Model, k string) bool {
	if k == "\x03" {
		return true
	}
	if m.Mode == modeConfirm {
		switch k {
		case "y", "Y":
			m.Mode = modeRunning
		case "n", "N", "\x1b", "q":
			m.Mode = modeBrowse
		}
		return false
	}

	switch k {
	case "q":
		return true
	case "j", "\x1b[B":
		m.move(1)
	case "k", "\x1b[A":
		m.move(-1)
	case " ":
		if r, ok := m.current(); ok {
			m.toggle(r)
		}
	case "\r", "\n":
		if r, ok := m.current(); ok {
			if r.item != nil {
				m.toggle(r)
			} else {
				m.setExpanded(r.group, !r.group.Expanded)
			}
		}
	case "l", "\x1b[C":
		if r, ok := m.current(); ok && r.item == nil {
			m.setExpanded(r.group, true)
		}
	case "h", "\x1b[D":
		if r, ok := m.current(); ok {
			m.setExpanded(r.group, false)
		}
	case "e", "E":
		m.toggleExpandAll()
	case "a":
		m.setAll(true)
	case "n":
		m.setAll(false)
	case "d", "p":
		if len(m.selected()) > 0 && !m.selectedScanning() {
			m.Mode = modeConfirm
		}
	}
	return false
}

// ---- canvas -------------------------------------------------------------
//
// Every frame paints a full W×H grid of single-width cells and writes it in
// one go. Alignment is counted in cells, never bytes, so colour codes can't
// push columns out of place.

type cell struct {
	r  rune
	st string
}

type canvas struct {
	w, h  int
	cells []cell
}

func newCanvas(w, h int) *canvas {
	c := &canvas{w: w, h: h, cells: make([]cell, w*h)}
	for i := range c.cells {
		c.cells[i] = cell{r: ' '}
	}
	return c
}

func (c *canvas) set(x, y int, r rune, st string) {
	if x < 0 || y < 0 || x >= c.w || y >= c.h {
		return
	}
	c.cells[y*c.w+x] = cell{r: r, st: st}
}

// put writes s starting at (x, y) and returns the column after the last rune.
func (c *canvas) put(x, y int, s, st string) int {
	for _, r := range s {
		c.set(x, y, r, st)
		x++
	}
	return x
}

func (c *canvas) fill(x, y, w int, r rune, st string) {
	for i := 0; i < w; i++ {
		c.set(x+i, y, r, st)
	}
}

func (c *canvas) String() string {
	var b strings.Builder
	b.Grow(c.w * c.h * 4)
	b.WriteString("\x1b[H")
	for y := 0; y < c.h; y++ {
		cur := ""
		b.WriteString(reset + bg)
		for x := 0; x < c.w; x++ {
			cl := c.cells[y*c.w+x]
			if cl.st != cur {
				b.WriteString(reset + bg)
				b.WriteString(cl.st)
				cur = cl.st
			}
			b.WriteRune(cl.r)
		}
		b.WriteString(reset)
		if y < c.h-1 {
			// Raw mode turns off output post-processing, so a bare \n does
			// not return to column 0. That is what staircased the old screen.
			b.WriteString("\r\n")
		}
	}
	return b.String()
}

// ---- view ---------------------------------------------------------------

type seg struct {
	text string
	st   string
}

// line is one row inside the panel: left segments flow from the left edge,
// right segments sit flush against the right edge.
type line struct {
	left  []seg
	right []seg
}

type view struct {
	frame int
	top   int // first visible list row, for scrolling
	lastW int
	lastH int
}

func (v *view) draw(m *Model) {
	w, h := termSize()
	if w != v.lastW || h != v.lastH {
		fmt.Print(bg + "\x1b[2J")
		v.lastW, v.lastH = w, h
	}

	c := newCanvas(w, h)
	drawRain(c, v.frame)

	if w < 56 || h < 16 {
		msg := " make the window a bit bigger =^･ｪ･^= "
		c.put(max(0, (w-runeLen(msg))/2), h/2, msg, bold+green)
		fmt.Print(c.String())
		return
	}

	pw := min(w-6, 92)
	inner := pw - 4

	header := v.header(m, inner)
	// Rows left for the body once the border, a strip of rain, the header
	// and the footer are taken out. Each screen fits its body to this, so
	// the footer with the key hints is never the part that gets cut off.
	room := func(footer []line) int { return max(h-len(header)-len(footer)-2-4, 3) }
	var body, footer []line
	switch m.Mode {
	case modeConfirm:
		footer = confirmFooter(m)
		body = confirmLines(m, room(footer))
	case modeRunning, modeDone:
		footer = runFooter(m)
		body = runLines(m, inner, room(footer), v.frame)
	default:
		footer = v.browseFooter(m, inner)
		body = v.browseLines(m, inner, room(footer))
	}

	lines := append(append(append([]line{}, header...), body...), footer...)
	if len(lines) > h-4 {
		lines = lines[:h-4]
	}
	ph := len(lines) + 2
	px := (w - pw) / 2
	py := max(1, (h-ph)/2)

	drawPanel(c, px, py, pw, ph)
	for i, ln := range lines {
		y := py + 1 + i
		x := px + 2
		for _, s := range ln.left {
			x = c.put(x, y, s.text, s.st)
		}
		rw := 0
		for _, s := range ln.right {
			rw += runeLen(s.text)
		}
		rx := px + 2 + inner - rw
		for _, s := range ln.right {
			rx = c.put(rx, y, s.text, s.st)
		}
	}

	fmt.Print(c.String())
}

func (v *view) header(m *Model, inner int) []line {
	cat := "=^･ｪ･^="
	if v.frame%40 < 2 {
		cat = "=^-ｪ-^=" // blink
	}
	right := []seg{{cat, pink}}
	if m.DryRun {
		right = append([]seg{{"dry-run  ", amber + bold}}, right...)
	}
	return []line{
		{left: []seg{{"D I S K P U R G E", green + bold}}, right: right},
		{left: []seg{{strings.Repeat("─", inner), shade}}},
		{},
	}
}

func (v *view) browseLines(m *Model, inner, room int) []line {
	rows := m.rows()

	if m.Cursor < v.top {
		v.top = m.Cursor
	}
	if m.Cursor >= v.top+room {
		v.top = m.Cursor - room + 1
	}
	v.top = max(0, min(v.top, len(rows)-room))

	end := min(len(rows), v.top+room)
	var out []line
	for i := v.top; i < end; i++ {
		out = append(out, rowLine(rows[i], i == m.Cursor, inner, v.frame))
	}
	return out
}

func rowLine(r row, active bool, inner, frame int) line {
	ptr := seg{"  ", ""}
	if active {
		ptr = seg{"▸ ", green + bold}
	}

	if r.item == nil {
		g := r.group
		glyph, col := stateGlyph(g.state())
		arrow := "▸ "
		if g.Expanded {
			arrow = "▾ "
		}
		name := seg{g.Name, bold + white}
		if active {
			name.st = bold + green
		}
		size := humanSize(g.selectedSize())
		if g.scanning() {
			size = spinner(frame) + " scanning"
		}
		return line{
			left: []seg{ptr, {arrow, forest}, {glyph + " ", col}, name},
			right: []seg{
				{fmt.Sprintf("%d/%d", g.selectedCount(), len(g.Items)), leaf},
				{"   " + padLeft(size, 10), mint},
			},
		}
	}

	it := r.item
	box, boxSt := "○", leaf
	if it.Selected {
		box, boxSt = "●", green
	}
	nameSt := white
	if active {
		nameSt = green + bold
	}
	size := spinner(frame)
	if it.Scanned {
		size = humanSize(it.Size)
	}

	const nameW = 20
	noteW := inner - 2 - 4 - 2 - nameW - 1 - 12
	left := []seg{ptr, {"    ", ""}, {box + " ", boxSt}, {padRight(shorten(it.Name, nameW), nameW) + " ", nameSt}}
	if noteW > 6 {
		left = append(left, seg{shorten(it.Note, noteW), leaf})
	}
	return line{left: left, right: []seg{{padLeft(size, 10), mint}}}
}

func (v *view) browseFooter(m *Model, inner int) []line {
	action := ""
	if r, ok := m.current(); ok && r.item != nil {
		action = "→ " + r.item.Action
	}
	return []line{
		{},
		{left: []seg{{strings.Repeat("─", inner), shade}}},
		{
			left: []seg{
				{fmt.Sprintf("%d", len(m.selected())), green + bold},
				{" selected", leaf},
			},
			right: []seg{
				{humanSize(m.selectedSize()), mint + bold},
				{" to reclaim", leaf},
			},
		},
		{left: []seg{{shorten(action, inner), mint}}},
		{left: keyHints(inner, !m.selectedScanning(), m.allExpanded())},
	}
}

// keyHints lays out the browse-mode keys, keys bright and labels quieter,
// with purge and quit coloured so they stand apart. Pairs that don't fit
// the panel width are dropped from the end. Purge waits until the selected
// items are measured; until then its hint says so.
func keyHints(inner int, ready, allOpen bool) []seg {
	expandLabel := "open all"
	if allOpen {
		expandLabel = "close all"
	}
	hints := []struct{ key, label, st string }{
		{"j/k", "move", white + bold},
		{"h/l", "fold", white + bold},
		{"e", expandLabel, white + bold},
		{"space", "pick", white + bold},
		{"a", "all", white + bold},
		{"n", "none", white + bold},
		{"d", "purge", red + bold},
		{"q", "quit", amber + bold},
	}
	if !ready {
		hints[6].label, hints[6].st = "purge (measuring…)", forest
	}
	var out []seg
	used := 0
	for i, h := range hints {
		sep := ""
		if i > 0 {
			sep = "  "
		}
		n := runeLen(sep + h.key + " " + h.label)
		if used+n > inner {
			break
		}
		used += n
		labelSt := leaf
		if h.st != white+bold {
			labelSt = h.st
		}
		out = append(out, seg{sep + h.key, h.st}, seg{" " + h.label, labelSt})
	}
	return out
}

func confirmLines(m *Model, room int) []line {
	sel := m.selected()
	body := []line{
		{left: []seg{{fmt.Sprintf("purge %d thing(s), %s ?", len(sel), humanSize(m.selectedSize())), red + bold}}},
		{},
	}
	show := len(sel)
	if len(sel) > room-2 {
		show = max(room-3, 0) // leave a row for the "more" line
	}
	for _, it := range sel[:show] {
		body = append(body, line{
			left:  []seg{{"  ▪ ", red}, {padRight(shorten(it.Name, 20), 20) + " ", white}, {it.Action, leaf}},
			right: []seg{{" " + padLeft(humanSize(it.Size), 10), mint}},
		})
	}
	if show < len(sel) {
		body = append(body, line{left: []seg{{fmt.Sprintf("  … and %d more", len(sel)-show), leaf}}})
	}
	return body
}

func confirmFooter(m *Model) []line {
	footer := []line{{}}
	if m.DryRun {
		footer = append(footer, line{left: []seg{{"dry-run: nothing will be deleted", amber}}})
	}
	return append(footer, line{left: []seg{{"y", green + bold}, {" do it   ", leaf}, {"n", green + bold}, {" back", leaf}}})
}

// runLines is the purge screen, while it runs and once it is done: one row
// per step with its command, and under the step that is running, the tail of
// what it printed.
func runLines(m *Model, inner, room, frame int) []line {
	var head line
	switch {
	case m.Mode == modeRunning && m.Stopped:
		head = line{left: []seg{{"stopping… ", amber + bold}, {spinner(frame), amber}}}
	case m.Mode == modeRunning:
		head = line{left: []seg{
			{fmt.Sprintf("purging %d/%d ", min(m.doneCount()+1, len(m.Results)), len(m.Results)), mint + bold},
			{spinner(frame), mint},
		}}
	default:
		verb := "freed"
		if m.DryRun {
			verb = "would free"
		}
		head = line{left: []seg{{verb + " ", green + bold}, {humanSize(m.Freed), mint + bold}, {"  ✧", pink}}}
		if m.Stopped {
			head.left = append(head.left, seg{"   stopped early", amber})
		}
	}

	listRoom := max(room-2, 1)
	// The output pane has a fixed height for the whole run, so the panel
	// doesn't jump as lines arrive.
	tailN := min(8, max(2, listRoom-len(m.Results)), listRoom-1)

	const nameW = 18
	actionW := inner - 4 - nameW - 1 - 2 - 11
	detailW := inner - 8

	var list []line
	focusStart, focusEnd := 0, 0
	for _, res := range m.Results {
		glyph, glyphSt, nameSt, cmdSt, info, infoSt := "·", forest, leaf, forest, "", leaf
		switch res.Status {
		case statusRunning:
			glyph, glyphSt, nameSt, cmdSt = spinner(frame), amber+bold, white+bold, mint
			info, infoSt = fmtDur(time.Since(res.Started)), amber
		case statusOK:
			glyph, glyphSt, nameSt, cmdSt = "✓", green, white, leaf
			info, infoSt = humanSize(res.Size), mint
			if res.Sizeless {
				info = "ok"
			}
		case statusFailed:
			glyph, glyphSt, nameSt, cmdSt = "✗", red, white, leaf
			info, infoSt = "failed", red
			if res.Err == errStopped {
				glyph, glyphSt, info, infoSt = "–", amber, "stopped", amber
			}
		case statusSkipped:
			glyph, info, infoSt = "–", "skipped", forest
		}
		row := line{
			left: []seg{
				{"  " + glyph + " ", glyphSt},
				{padRight(shorten(res.Name, nameW), nameW) + " ", nameSt},
				{"$ " + shorten(res.Action, actionW-2), cmdSt},
			},
			right: []seg{{padLeft(info, 10), infoSt}},
		}
		if actionW < 6 {
			row.left = row.left[:2]
		}

		var detail []line
		switch res.Status {
		case statusRunning:
			tail := res.Log.Tail(tailN)
			for i := 0; i < tailN; i++ {
				text := ""
				if i < len(tail) {
					text = shorten(tail[i], detailW)
				}
				detail = append(detail, line{left: []seg{{"      │ ", forest}, {text, leaf}}})
			}
		case statusFailed:
			for _, t := range res.Log.Tail(3) {
				detail = append(detail, line{left: []seg{{"      │ ", forest}, {shorten(t, detailW), leaf}}})
			}
			if res.Err != errStopped {
				detail = append(detail, line{left: []seg{{"      ✗ ", red}, {shorten(res.Err.Error(), detailW), red}}})
			}
		}

		if res.Status == statusRunning || (res.Status == statusFailed && focusEnd == 0) {
			focusStart, focusEnd = len(list), len(list)+1+len(detail)
		}
		list = append(list, row)
		list = append(list, detail...)
	}

	// Too many rows for the window: slide it so the running step (or the
	// first failure) and its output stay in view.
	if len(list) > listRoom {
		start := max(0, focusEnd-listRoom)
		start = min(start, focusStart, len(list)-listRoom)
		list = list[start : start+listRoom]
	}
	return append([]line{head, {}}, list...)
}

func runFooter(m *Model) []line {
	var hint []seg
	switch {
	case m.Mode == modeDone:
		hint = []seg{{"press any key to leave  =^･ｪ･^=ﾉ", leaf}}
	case m.Stopped:
		hint = []seg{{"ctrl-c", red + bold}, {" again to quit right now", leaf}}
	case m.asking():
		// The command (sudo) is waiting for a password. It reads the keys
		// itself; this field only shows that typing is getting through.
		hint = []seg{
			{"password ", amber + bold}, {strings.Repeat("•", m.Typed) + "▏", white + bold},
			{"   enter", white + bold}, {" confirm   ", leaf}, {"ctrl-c", red + bold}, {" stop", red},
		}
	default:
		hint = []seg{{"ctrl-c", red + bold}, {" stop", red}, {"   other keys go to the running command", leaf}}
	}
	return []line{{}, {left: hint}}
}

func fmtDur(d time.Duration) string {
	s := int(d.Seconds())
	if s < 60 {
		return fmt.Sprintf("%ds", s)
	}
	return fmt.Sprintf("%dm%02ds", s/60, s%60)
}

func drawPanel(c *canvas, x, y, w, h int) {
	for row := y; row < y+h; row++ {
		c.fill(x, row, w, ' ', "")
	}
	c.set(x, y, '╭', forest)
	c.set(x+w-1, y, '╮', forest)
	c.set(x, y+h-1, '╰', forest)
	c.set(x+w-1, y+h-1, '╯', forest)
	c.fill(x+1, y, w-2, '─', forest)
	c.fill(x+1, y+h-1, w-2, '─', forest)
	for row := y + 1; row < y+h-1; row++ {
		c.set(x, row, '│', forest)
		c.set(x+w-1, row, '│', forest)
	}
}

// drawRain paints falling glyph columns across the whole screen. Each column
// takes its speed, length and phase from a hash, so the motion is smooth and
// needs no state between frames.
func drawRain(c *canvas, frame int) {
	glyphs := []rune(rainChars)
	trail := []string{white + bold, green, leaf, forest, forest, shade, shade, shade}
	for x := 0; x < c.w; x++ {
		hx := hash(x, 7, 11)
		if hx%3 == 0 {
			continue
		}
		speed := 1 + int(hx>>4)%3
		length := 6 + int(hx>>8)%10
		cycle := c.h + length + int(hx>>12)%c.h
		head := (frame*speed/2 + int(hx>>16)) % cycle
		for k := 0; k < length; k++ {
			y := head - k
			if y < 0 || y >= c.h {
				continue
			}
			st := trail[min(k*len(trail)/length, len(trail)-1)]
			g := glyphs[int(hash(x, y, frame/5+k))%len(glyphs)]
			c.set(x, y, g, st)
		}
	}
}

func hash(a, b, c int) uint32 {
	h := uint32(a)*0x9E3779B1 ^ uint32(b)*0x85EBCA77 ^ uint32(c)*0xC2B2AE3D
	h ^= h >> 15
	h *= 0x2C1B3C6D
	h ^= h >> 12
	return h
}

func stateGlyph(s checkState) (string, string) {
	switch s {
	case checkAll:
		return "◉", green
	case checkSome:
		return "◐", mint
	default:
		return "○", leaf
	}
}

func spinner(frame int) string {
	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	return frames[frame%len(frames)]
}

func runeLen(s string) int { return len([]rune(s)) }

func padRight(s string, n int) string {
	if d := n - runeLen(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

func padLeft(s string, n int) string {
	if d := n - runeLen(s); d > 0 {
		return strings.Repeat(" ", d) + s
	}
	return s
}

func shorten(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for q := n / unit; q >= unit; q /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

type winsize struct{ row, col, x, y uint16 }

func termSize() (int, int) {
	var ws winsize
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, os.Stdout.Fd(),
		uintptr(syscall.TIOCGWINSZ), uintptr(unsafe.Pointer(&ws)))
	if errno != 0 || ws.col == 0 || ws.row == 0 {
		return 80, 24
	}
	return int(ws.col), int(ws.row)
}

// stty runs stty against the controlling terminal and returns its output.
func stty(args ...string) (string, error) {
	cmd := exec.Command("stty", args...)
	cmd.Stdin = os.Stdin
	out, err := cmd.Output()
	return string(out), err
}
