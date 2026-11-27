package main

import "time"

type Mode int

const (
	modeBrowse Mode = iota
	modeConfirm
	modeRunning
	modeDone
)

// Item is one thing that can be purged.
type Item struct {
	Name     string
	Path     string // directory measured for size
	Note     string
	Action   string // what the purge does, shown to the user
	Selected bool
	Size     int64
	Scanned  bool
	Done     bool
	Sizeless bool // frees nothing itself (the sudo step): no size is shown
	purge    func(*job) error
}

// Group is a tier of items. Selecting the group selects all of its items.
type Group struct {
	Name     string
	Items    []*Item
	Expanded bool
}

type Status int

const (
	statusPending Status = iota
	statusRunning
	statusOK
	statusFailed
	statusSkipped
)

// Result is one purge step: what ran, how far it got, and what it printed.
type Result struct {
	Name     string
	Action   string
	Size     int64
	Sizeless bool // the sudo step: shows "ok" rather than a size
	Err      error
	Status   Status
	Log      termLog
	Started  time.Time
	Took     time.Duration
	Asking   bool // its command is waiting for a password
}

type Model struct {
	Groups  []*Group
	Cursor  int
	Mode    Mode
	DryRun  bool
	Sudo    bool // ask for the sudo password before a real run starts
	Results []*Result
	Freed   int64
	Stopped bool // the user stopped the run part-way
	Typed   int  // characters typed into the password field so far

	running []*Item // items of the current run, parallel to Results
}

// row is one visible line in the tree: a group (item == nil) or an item.
type row struct {
	group *Group
	item  *Item
}

type checkState int

const (
	checkNone checkState = iota
	checkSome
	checkAll
)

// NewModel starts with every group open, so all items are visible at once.
func NewModel(groups []*Group, dryRun bool) *Model {
	for _, g := range groups {
		g.Expanded = true
	}
	return &Model{Groups: groups, DryRun: dryRun}
}

func (g *Group) selectedCount() int {
	n := 0
	for _, it := range g.Items {
		if it.Selected {
			n++
		}
	}
	return n
}

func (g *Group) selectedSize() int64 {
	var total int64
	for _, it := range g.Items {
		if it.Selected {
			total += it.Size
		}
	}
	return total
}

func (g *Group) state() checkState {
	n := g.selectedCount()
	switch {
	case n == 0:
		return checkNone
	case n == len(g.Items):
		return checkAll
	default:
		return checkSome
	}
}

func (g *Group) setAll(on bool) {
	for _, it := range g.Items {
		it.Selected = on
	}
}

func (g *Group) scanning() bool {
	for _, it := range g.Items {
		if !it.Scanned {
			return true
		}
	}
	return false
}

func (m *Model) rows() []row {
	var rows []row
	for _, g := range m.Groups {
		rows = append(rows, row{group: g})
		if g.Expanded {
			for _, it := range g.Items {
				rows = append(rows, row{group: g, item: it})
			}
		}
	}
	return rows
}

func (m *Model) current() (row, bool) {
	rows := m.rows()
	if m.Cursor < 0 || m.Cursor >= len(rows) {
		return row{}, false
	}
	return rows[m.Cursor], true
}

func (m *Model) move(delta int) {
	n := len(m.rows())
	m.Cursor += delta
	if m.Cursor < 0 {
		m.Cursor = 0
	}
	if m.Cursor >= n {
		m.Cursor = n - 1
	}
}

// toggle flips one item, or flips a whole group: a group that is not fully
// selected becomes fully selected; a fully selected group is cleared.
func (m *Model) toggle(r row) {
	if r.item != nil {
		r.item.Selected = !r.item.Selected
		return
	}
	r.group.setAll(r.group.state() != checkAll)
}

func (m *Model) setAll(on bool) {
	for _, g := range m.Groups {
		g.setAll(on)
	}
}

// setExpanded opens or closes a group. Closing moves the cursor onto the group
// row so it never points at a hidden child.
func (m *Model) setExpanded(g *Group, open bool) {
	g.Expanded = open
	if open {
		return
	}
	for i, r := range m.rows() {
		if r.group == g && r.item == nil {
			m.Cursor = i
			return
		}
	}
}

// allExpanded reports whether every group is open.
func (m *Model) allExpanded() bool {
	for _, g := range m.Groups {
		if !g.Expanded {
			return false
		}
	}
	return true
}

// toggleExpandAll opens every group, or closes them all if they are already
// all open. The cursor stays on the row it was on; if that row is an item
// that just got hidden, it moves up to the item's group.
func (m *Model) toggleExpandAll() {
	cur, had := m.current()
	open := !m.allExpanded()
	for _, g := range m.Groups {
		g.Expanded = open
	}
	if !had {
		return
	}
	for i, r := range m.rows() {
		if r.group == cur.group && (r.item == cur.item || r.item == nil && !open) {
			m.Cursor = i
			return
		}
	}
}

func (m *Model) allItems() []*Item {
	var items []*Item
	for _, g := range m.Groups {
		items = append(items, g.Items...)
	}
	return items
}

func (m *Model) selected() []*Item {
	var sel []*Item
	for _, it := range m.allItems() {
		if it.Selected {
			sel = append(sel, it)
		}
	}
	return sel
}

func (m *Model) selectedSize() int64 {
	var total int64
	for _, it := range m.selected() {
		total += it.Size
	}
	return total
}

// selectedScanning reports whether a selected item is still being measured.
// Purging waits for that, so the confirm screen and the freed total are right.
func (m *Model) selectedScanning() bool {
	for _, it := range m.selected() {
		if !it.Scanned {
			return true
		}
	}
	return false
}

// Begin switches to the running screen and returns the items to purge. The
// caller runs them and feeds the progress back through Apply. With Sudo set,
// a real run starts by asking for the sudo password.
func (m *Model) Begin() []*Item {
	m.running = m.selected()
	if m.Sudo && !m.DryRun {
		m.running = append([]*Item{sudoItem()}, m.running...)
	}
	m.Results = nil
	m.Freed = 0
	m.Stopped = false
	for _, it := range m.running {
		m.Results = append(m.Results, &Result{Name: it.Name, Action: it.Action, Size: it.Size, Sizeless: it.Sizeless})
	}
	m.Mode = modeRunning
	return m.running
}

// Apply records one progress message from the run. In dry-run mode nothing
// was touched; results still report what would have run.
func (m *Model) Apply(msg runMsg) {
	if msg.kind == runAll {
		m.Mode = modeDone
		return
	}
	if msg.idx < 0 || msg.idx >= len(m.Results) {
		return
	}
	res, it := m.Results[msg.idx], m.running[msg.idx]
	switch msg.kind {
	case runStart:
		res.Status = statusRunning
		res.Started = time.Now()
	case runOut:
		res.Log.Write(msg.data)
	case runAsk:
		res.Asking = msg.ask
		m.Typed = 0
	case runSkip:
		res.Status = statusSkipped
	case runEnd:
		res.Took = time.Since(res.Started)
		res.Err = msg.err
		if msg.err != nil {
			res.Status = statusFailed
			return
		}
		res.Status = statusOK
		m.Freed += res.Size
		if !m.DryRun {
			it.Done = true
			it.Selected = false
			it.Size = 0
		}
	}
}

// asking reports whether the running step is waiting for a password.
func (m *Model) asking() bool {
	for _, r := range m.Results {
		if r.Status == statusRunning && r.Asking {
			return true
		}
	}
	return false
}

// noteTyped keeps the masked password field in step with the keys that were
// just passed to the command. Only the count is kept, never the keys.
func (m *Model) noteTyped(keys []string) {
	if !m.asking() {
		return
	}
	for _, k := range keys {
		switch {
		case k == "\r" || k == "\n" || k == "\x15":
			m.Typed = 0
		case k == "\x7f" || k == "\b":
			m.Typed = max(m.Typed-1, 0)
		case k[0] >= 0x20:
			m.Typed++
		}
	}
}

// doneCount is how many steps have finished, whatever the outcome.
func (m *Model) doneCount() int {
	n := 0
	for _, r := range m.Results {
		if r.Status != statusPending && r.Status != statusRunning {
			n++
		}
	}
	return n
}
