package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
	"unsafe"
)

// Palette and canvas follow diskpurge.
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
	bg     = "\x1b[48;5;233m"
)

const rainChars = "ｱｲｳｴｵｶｷｸｹｺｻｼｽｾｿﾀﾁﾂﾃﾄﾅﾆﾇﾈﾉﾊﾋﾌﾍﾎﾏﾐﾑﾒﾓﾔﾕﾖﾗﾘﾙﾚﾛﾜﾝ0123456789"

type runMsg struct {
	item *Item
	done bool // whole run finished
}

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

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sigs)

	keys := make(chan string)
	go readKeys(keys)

	ticker := time.NewTicker(80 * time.Millisecond)
	defer ticker.Stop()
	v := &view{}
	events := make(chan runMsg)
	var doneAt time.Time

	v.draw(m)
	for {
		select {
		case raw, ok := <-keys:
			if !ok {
				return nil
			}
			switch m.Mode {
			case modeRunning:
				// git commands are quick; let them finish.
			case modeDone:
				if time.Since(doneAt) > 400*time.Millisecond {
					return nil
				}
			default:
				for _, k := range splitKeys(raw) {
					if quit := handleKey(m, k); quit {
						return nil
					}
					if m.Mode == modeRunning {
						m.Run = m.selected()
						go runPrune(m.Run, events)
						break
					}
				}
			}
		case e := <-events:
			if e.done {
				m.Mode = modeDone
				doneAt = time.Now()
			}
		case <-sigs:
			return nil
		case <-ticker.C:
			v.frame++
		}
		v.draw(m)
	}
}

func runPrune(items []*Item, out chan<- runMsg) {
	for _, it := range items {
		it.Status = statusRunning
		out <- runMsg{item: it}
		if err := prune(it); err != nil {
			it.Status, it.Err = statusFailed, err
		} else {
			it.Status = statusOK
		}
		out <- runMsg{item: it}
	}
	_, _ = git("", "worktree", "prune")
	out <- runMsg{done: true}
}

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
	case "q", "\x1b":
		return true
	case "j", "\x1b[B":
		m.move(1)
	case "k", "\x1b[A":
		m.move(-1)
	case "g", "\x1b[H":
		m.Cursor = 0
	case "G", "\x1b[F":
		m.Cursor = len(m.rows()) - 1
	case " ":
		if r, ok := m.current(); ok {
			m.toggle(r)
			if r.item != nil {
				m.move(1)
			}
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
		m.selectMerged()
	case "n":
		m.selectNone()
	case "p", "d":
		if len(m.selected()) > 0 {
			m.Mode = modeConfirm
		}
	}
	return false
}

// ---- view ---------------------------------------------------------------

type seg struct {
	text string
	st   string
}

type line struct {
	left  []seg
	right []seg
}

type view struct {
	frame        int
	top          int
	lastW, lastH int
}

func (v *view) draw(m *Model) {
	w, h := termSize()
	if w != v.lastW || h != v.lastH {
		fmt.Print(bg + "\x1b[2J")
		v.lastW, v.lastH = w, h
	}
	c := newCanvas(w, h)
	drawRain(c, v.frame)

	if w < 60 || h < 16 {
		msg := " make the window a bit bigger "
		c.put(max(0, (w-runeLen(msg))/2), h/2, msg, bold+green)
		fmt.Print(c.String())
		return
	}

	pw := min(w-4, 132)
	inner := pw - 4
	header := v.header(m, inner)
	room := func(footer []line) int { return max(h-len(header)-len(footer)-2-2, 3) }

	var body, footer []line
	switch m.Mode {
	case modeConfirm:
		footer = []line{{}, {left: []seg{{"y", green + bold}, {" prune   ", leaf}, {"n", green + bold}, {" back", leaf}}}}
		body = confirmLines(m, inner, room(footer))
	case modeRunning, modeDone:
		footer = runFooter(m)
		body = runLines(m, inner, room(footer), v.frame)
	default:
		footer = v.browseFooter(m, inner)
		body = v.browseLines(m, inner, room(footer))
	}

	lines := append(append(append([]line{}, header...), body...), footer...)
	if len(lines) > h-2 {
		lines = lines[:h-2]
	}
	ph := len(lines) + 2
	px := (w - pw) / 2
	py := max(0, (h-ph)/2)

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
		cat = "=^-ｪ-^="
	}
	return []line{
		{left: []seg{{"G I T P R U N E", green + bold}}, right: []seg{{cat, pink}}},
		{
			left:  []seg{{filepath.Base(m.Repo.top), white + bold}, {"  " + shorten(m.Repo.top, inner-40), leaf}},
			right: []seg{{"base ", leaf}, {m.Repo.baseRef, mint + bold}},
		},
		{left: []seg{{strings.Repeat("─", inner), shade}}},
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

	nameW := 12
	for _, r := range rows {
		if r.item != nil {
			nameW = max(nameW, runeLen(r.item.B.name))
		}
	}
	nameW = min(nameW, inner/2-6)

	var out []line
	for i := v.top; i < end; i++ {
		out = append(out, rowLine(rows[i], i == m.Cursor, inner, nameW))
	}
	return out
}

func rowLine(r row, active bool, inner, nameW int) line {
	ptr := seg{"  ", ""}
	if active {
		ptr = seg{"▸ ", green + bold}
	}
	if r.item == nil {
		g := r.group
		glyph, col := stateGlyph(g.state())
		if len(g.pickable()) == 0 {
			glyph, col = "·", forest
		}
		arrow := "▸ "
		if g.Expanded {
			arrow = "▾ "
		}
		name := seg{g.Name, bold + white}
		if active {
			name.st = bold + green
		}
		return line{
			left:  []seg{ptr, {arrow, forest}, {glyph + " ", col}, name},
			right: []seg{{fmt.Sprintf("%d/%d", g.selectedCount(), len(g.Items)), leaf}},
		}
	}

	it := r.item
	box, boxSt := "○", leaf
	switch {
	case it.Locked:
		box, boxSt = "·", forest
	case it.Selected && len(it.Warn) > 0:
		box, boxSt = "●", amber
	case it.Selected:
		box, boxSt = "●", green
	}
	nameSt := white
	if it.Locked {
		nameSt = forest
	}
	if active {
		nameSt = green + bold
	}
	date := it.B.date
	noteW := inner - 2 - 4 - 2 - nameW - 2 - runeLen(date) - 2
	left := []seg{ptr, {"    ", ""}, {box + " ", boxSt}, {padRight(shorten(it.B.name, nameW), nameW) + "  ", nameSt}}
	if noteW > 6 {
		left = append(left, seg{shorten(it.Note, noteW), leaf})
	}
	return line{left: left, right: []seg{{date, forest}}}
}

func (v *view) browseFooter(m *Model, inner int) []line {
	var detail []seg
	if r, ok := m.current(); ok && r.item != nil {
		it := r.item
		switch {
		case len(it.Warn) > 0:
			detail = []seg{{"! ", amber + bold}, {shorten(strings.Join(it.Warn, " · "), inner-2), amber}}
		case it.Why != "":
			detail = []seg{{"✓ ", green}, {shorten(it.Why, inner-2), mint}}
		}
		if it.B.worktree != "" && !it.Locked {
			detail = append(detail, seg{shorten("   wt "+it.B.worktree, max(0, inner-segLen(detail))), leaf})
		}
	}
	sel := []seg{{fmt.Sprintf("%d", len(m.selected())), green + bold}, {" selected", leaf}}
	if n := m.risky(); n > 0 {
		sel = append(sel, seg{fmt.Sprintf("   %d with unmerged work", n), amber})
	}
	return []line{
		{},
		{left: []seg{{strings.Repeat("─", inner), shade}}},
		{left: sel},
		{left: detail},
		{left: keyHints(inner, m.allExpanded())},
	}
}

func keyHints(inner int, allOpen bool) []seg {
	expand := "open all"
	if allOpen {
		expand = "close all"
	}
	hints := []struct{ key, label, st string }{
		{"j/k", "move", white + bold},
		{"h/l", "fold", white + bold},
		{"e", expand, white + bold},
		{"space", "pick", white + bold},
		{"a", "merged", white + bold},
		{"n", "none", white + bold},
		{"p", "prune", red + bold},
		{"q", "quit", amber + bold},
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

func confirmLines(m *Model, inner, room int) []line {
	sel := m.selected()
	head := fmt.Sprintf("prune %d branch(es)?", len(sel))
	body := []line{{left: []seg{{head, red + bold}, {"  worktree removed, branch deleted", leaf}}}, {}}
	show := len(sel)
	if len(sel) > room-2 {
		show = max(room-3, 0)
	}
	nameW := 0
	for _, it := range sel {
		nameW = max(nameW, runeLen(it.B.name))
	}
	nameW = min(nameW, inner/2)
	for _, it := range sel[:show] {
		note, st := it.Why, leaf
		if len(it.Warn) > 0 {
			note, st = "! "+strings.Join(it.Warn, " · "), amber
		}
		if it.B.worktree != "" {
			note += "  + wt"
		}
		body = append(body, line{left: []seg{
			{"  ▪ ", red}, {padRight(shorten(it.B.name, nameW), nameW) + "  ", white},
			{shorten(note, inner-nameW-6), st},
		}})
	}
	if show < len(sel) {
		body = append(body, line{left: []seg{{fmt.Sprintf("  … and %d more", len(sel)-show), leaf}}})
	}
	return body
}

func runLines(m *Model, inner, room, frame int) []line {
	var head line
	if m.Mode == modeRunning {
		head = line{left: []seg{{fmt.Sprintf("pruning %d/%d ", min(m.doneCount()+1, len(m.Run)), len(m.Run)), mint + bold}, {spinner(frame), mint}}}
	} else {
		ok := 0
		for _, it := range m.Run {
			if it.Status == statusOK {
				ok++
			}
		}
		head = line{left: []seg{{"pruned ", green + bold}, {fmt.Sprintf("%d", ok), mint + bold}, {"  ✧", pink}}}
		if f := len(m.Run) - ok; f > 0 {
			head.left = append(head.left, seg{fmt.Sprintf("   %d failed", f), red})
		}
	}
	var list []line
	focus := 0
	for i, it := range m.Run {
		glyph, gst, info, ist := "·", forest, "", leaf
		switch it.Status {
		case statusRunning:
			glyph, gst, focus = spinner(frame), amber+bold, i
		case statusOK:
			glyph, gst, info, ist = "✓", green, "pruned", mint
		case statusFailed:
			glyph, gst, info, ist = "✗", red, "failed", red
		}
		list = append(list, line{
			left:  []seg{{"  " + glyph + " ", gst}, {shorten(it.B.name, inner-14), white}},
			right: []seg{{info, ist}},
		})
		if it.Err != nil {
			list = append(list, line{left: []seg{{"      ✗ ", red}, {shorten(it.Err.Error(), inner-8), red}}})
		}
	}
	listRoom := max(room-2, 1)
	if len(list) > listRoom {
		start := min(max(0, focus-listRoom/2), len(list)-listRoom)
		if m.Mode == modeDone {
			start = len(list) - listRoom
		}
		list = list[start : start+listRoom]
	}
	return append([]line{head, {}}, list...)
}

func runFooter(m *Model) []line {
	if m.Mode == modeDone {
		return []line{{}, {left: []seg{{"press any key  → list what's left  =^･ｪ･^=ﾉ", leaf}}}}
	}
	return []line{{}, {left: []seg{{"working…", leaf}}}}
}

// ---- canvas -------------------------------------------------------------

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
				b.WriteString(reset + bg + cl.st)
				cur = cl.st
			}
			b.WriteRune(cl.r)
		}
		b.WriteString(reset)
		if y < c.h-1 {
			b.WriteString("\r\n")
		}
	}
	return b.String()
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

func runeLen(s string) int { return utf8.RuneCountInString(s) }

func segLen(ss []seg) int {
	n := 0
	for _, s := range ss {
		n += runeLen(s.text)
	}
	return n
}

func padRight(s string, n int) string {
	if d := n - runeLen(s); d > 0 {
		return s + strings.Repeat(" ", d)
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

func stty(args ...string) (string, error) {
	cmd := exec.Command("stty", args...)
	cmd.Stdin = os.Stdin
	out, err := cmd.Output()
	return string(out), err
}
