package main

type Mode int

const (
	modeBrowse Mode = iota
	modeConfirm
	modeRunning
	modeDone
)

type Status int

const (
	statusPending Status = iota
	statusRunning
	statusOK
	statusFailed
)

// Item is one local branch.
type Item struct {
	B        branch
	Note     string   // status line
	Why      string   // why it counts as merged, or where it's checked out
	Warn     []string // what pruning it would lose
	Locked   bool     // checked out here or in the main worktree
	Selected bool
	Status   Status
	Err      error
}

// Group is a set of branches in the same state. Picking the group picks all
// of its items that can be pruned.
type Group struct {
	Name     string
	Items    []*Item
	Expanded bool
}

type Model struct {
	Groups []*Group
	Cursor int
	Mode   Mode
	Repo   *repo
	Run    []*Item
}

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

func NewModel(groups []*Group, r *repo) *Model {
	for _, g := range groups {
		g.Expanded = true
	}
	return &Model{Groups: groups, Repo: r}
}

func (g *Group) pickable() []*Item {
	var out []*Item
	for _, it := range g.Items {
		if !it.Locked {
			out = append(out, it)
		}
	}
	return out
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

func (g *Group) state() checkState {
	n, all := g.selectedCount(), len(g.pickable())
	switch n {
	case 0:
		return checkNone
	case all:
		return checkAll
	default:
		return checkSome
	}
}

func (g *Group) setAll(on bool) {
	for _, it := range g.pickable() {
		it.Selected = on
	}
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
	m.Cursor = max(0, min(m.Cursor+delta, len(m.rows())-1))
}

func (m *Model) toggle(r row) {
	if r.item != nil {
		if !r.item.Locked {
			r.item.Selected = !r.item.Selected
		}
		return
	}
	r.group.setAll(r.group.state() != checkAll)
}

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

func (m *Model) allExpanded() bool {
	for _, g := range m.Groups {
		if !g.Expanded {
			return false
		}
	}
	return true
}

func (m *Model) toggleExpandAll() {
	open := !m.allExpanded()
	r, _ := m.current()
	for _, g := range m.Groups {
		g.Expanded = open
	}
	// Keep the cursor on the same group.
	for i, x := range m.rows() {
		if x.group == r.group && x.item == nil {
			m.Cursor = i
			return
		}
	}
}

// selectMerged picks exactly the plain "merged" group.
func (m *Model) selectMerged() {
	for _, g := range m.Groups {
		g.setAll(g.Name == "merged")
	}
}

func (m *Model) selectNone() {
	for _, g := range m.Groups {
		g.setAll(false)
	}
}

func (m *Model) selected() []*Item {
	var out []*Item
	for _, g := range m.Groups {
		for _, it := range g.Items {
			if it.Selected {
				out = append(out, it)
			}
		}
	}
	return out
}

func (m *Model) risky() int {
	n := 0
	for _, it := range m.selected() {
		if len(it.Warn) > 0 {
			n++
		}
	}
	return n
}

func (m *Model) doneCount() int {
	n := 0
	for _, it := range m.Run {
		if it.Status == statusOK || it.Status == statusFailed {
			n++
		}
	}
	return n
}
