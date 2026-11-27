// gitprune lists the local branches of the current repo in groups (merged,
// open PR, not merged, …), lets you pick some with space and prunes them
// with p: worktree removed, branch deleted. When it exits it prints what is
// left in local with each branch's status.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Files that ~/.local/bin/agents-setup adds to a worktree. A worktree whose
// only changes are these still counts as clean.
var setupFiles = map[string]bool{
	"AGENTS.md":                       true,
	"CLAUDE.md":                       true,
	".agents/":                        true,
	".cursor/rules/shared-agents.mdc": true,
	".claude/":                        true,
}

type branch struct {
	name     string
	tip      string
	upstream string
	track    string // "[gone]", "[ahead 1, behind 2]", ""
	worktree string
	date     string
}

type pr struct {
	Number      int    `json:"number"`
	State       string `json:"state"`
	HeadRefName string `json:"headRefName"`
	HeadRefOid  string `json:"headRefOid"`
	URL         string `json:"url"`
}

// repo is what every screen needs to know about the repo being pruned.
type repo struct {
	top     string // worktree gitprune runs in
	mainWT  string
	web     string
	baseRef string // origin/staging
	cur     string // branch checked out here
	prs     map[string]*pr
}

func main() {
	base := flag.String("base", "", "base branch (default: origin's HEAD, e.g. staging)")
	noFetch := flag.Bool("no-fetch", false, "skip git fetch --prune")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "gitprune - pick local branches and prune them (worktree + branch)")
		fmt.Fprintln(os.Stderr, "runs on the git repo of the current directory")
		fmt.Fprintln(os.Stderr)
		flag.PrintDefaults()
	}
	flag.Parse()

	r, err := openRepo(*base, !*noFetch)
	if err != nil {
		fail(err.Error())
	}

	groups := classify(r)
	if len(groups) == 0 {
		fmt.Println("no local branches besides the base. already clean.")
	} else if err := runTUI(NewModel(groups, r)); err != nil {
		fail(err.Error())
	}
	printLeft(r)
}

func openRepo(base string, fetch bool) (*repo, error) {
	top, err := git("", "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("not in a git repo")
	}
	common, _ := git("", "rev-parse", "--path-format=absolute", "--git-common-dir")
	r := &repo{top: top, mainWT: filepath.Dir(common), web: repoURL()}

	if fetch {
		fmt.Fprintln(os.Stderr, "fetching origin --prune …")
		if _, err := git("", "fetch", "--prune", "--quiet", "origin"); err != nil {
			fmt.Fprintln(os.Stderr, "fetch failed, using local refs:", err)
		}
	}
	r.baseRef = "origin/" + strings.TrimPrefix(base, "origin/")
	if base == "" {
		head, err := git("", "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
		if err != nil {
			return nil, fmt.Errorf("cannot find origin/HEAD; pass -base staging")
		}
		r.baseRef = head
	}
	r.cur, _ = git("", "branch", "--show-current")
	fmt.Fprintln(os.Stderr, "reading PRs …")
	r.prs = loadPRs()
	return r, nil
}

// classify sorts every local branch (except the base) into a group. Only the
// plainly merged, clean ones start selected.
func classify(r *repo) []*Group {
	order := []string{
		"merged",
		"merged · worktree has changes",
		"merged PR · new commits since",
		"fresh worktree · no commits yet",
		"open PR",
		"not merged",
		"in use · can't prune",
	}
	byName := map[string]*Group{}
	baseName := strings.TrimPrefix(r.baseRef, "origin/")

	for _, b := range listBranches() {
		if b.name == baseName {
			continue
		}
		p := r.prs[b.name]
		why := mergedWhy(b, r.baseRef, p)
		d := ""
		if b.worktree != "" {
			d = dirty(b.worktree)
		}
		ahead := aheadOf(r.baseRef, b.tip)

		it := &Item{B: b, Note: statusText(b, r, false)}
		var g string
		switch {
		case b.name == r.cur || b.worktree == r.top || b.worktree == r.mainWT:
			g, it.Locked = "in use · can't prune", true
			it.Why = "checked out in " + b.worktree
		case why != "" && b.worktree != "" && strings.HasPrefix(why, "in ") && (p == nil || p.State != "MERGED"):
			g = "fresh worktree · no commits yet"
		case why != "" && d == "":
			g, it.Selected = "merged", true
		case why != "":
			g = "merged · worktree has changes"
		case p != nil && p.State == "MERGED":
			g = "merged PR · new commits since"
		case p != nil && p.State == "OPEN":
			g = "open PR"
		default:
			g = "not merged"
		}
		if why != "" && !it.Locked {
			it.Why = why
		}
		if why == "" && ahead > 0 {
			it.Warn = append(it.Warn, fmt.Sprintf("%d commit(s) not in %s", ahead, r.baseRef))
		}
		if d != "" {
			it.Warn = append(it.Warn, "worktree changes: "+d)
		}
		if byName[g] == nil {
			byName[g] = &Group{Name: g}
		}
		byName[g].Items = append(byName[g].Items, it)
	}

	var gs []*Group
	for _, n := range order {
		if g := byName[n]; g != nil {
			gs = append(gs, g)
		}
	}
	return gs
}

// prune removes the branch's worktree (if any) and deletes the branch.
func prune(it *Item) error {
	if it.B.worktree != "" {
		if _, err := git("", "worktree", "remove", "--force", it.B.worktree); err != nil {
			return err
		}
	}
	_, err := git("", "branch", "-D", it.B.name)
	return err
}

func mergedWhy(b branch, baseRef string, p *pr) string {
	if _, err := git("", "merge-base", "--is-ancestor", b.tip, baseRef); err == nil {
		return "in " + baseRef
	}
	if p != nil && p.State == "MERGED" && p.HeadRefOid == b.tip {
		return fmt.Sprintf("PR #%d merged", p.Number)
	}
	// Squash/rebase merge without a PR match: put the branch's tree on its
	// merge base and ask whether the base already has an equal patch.
	mb, err := git("", "merge-base", baseRef, b.tip)
	if err != nil {
		return ""
	}
	tmp, err := git("", "commit-tree", b.tip+"^{tree}", "-p", mb, "-m", "gitprune")
	if err != nil {
		return ""
	}
	out, err := git("", "cherry", baseRef, tmp)
	if err == nil && strings.HasPrefix(out, "-") {
		return "squashed into " + baseRef
	}
	return ""
}

func aheadOf(baseRef, tip string) int {
	out, err := git("", "rev-list", "--count", baseRef+".."+tip)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(out)
	return n
}

func listBranches() []branch {
	out, _ := git("", "for-each-ref", "refs/heads", "--sort=refname",
		"--format=%(refname:short)\t%(objectname)\t%(upstream:short)\t%(upstream:track)\t%(committerdate:relative)")
	wts := worktrees()
	var bs []branch
	for _, line := range lines(out) {
		f := strings.Split(line, "\t")
		if len(f) < 5 {
			continue
		}
		bs = append(bs, branch{name: f[0], tip: f[1], upstream: f[2], track: f[3], date: f[4], worktree: wts[f[0]]})
	}
	return bs
}

// worktrees maps branch name -> worktree path.
func worktrees() map[string]string {
	out, _ := git("", "worktree", "list", "--porcelain")
	m := map[string]string{}
	var path string
	for _, l := range lines(out) {
		switch {
		case strings.HasPrefix(l, "worktree "):
			path = strings.TrimPrefix(l, "worktree ")
		case strings.HasPrefix(l, "branch refs/heads/"):
			m[strings.TrimPrefix(l, "branch refs/heads/")] = path
		}
	}
	return m
}

// dirty returns a short summary of real changes in a worktree, ignoring
// agents-setup files. Empty means clean.
func dirty(path string) string {
	out, err := git(path, "status", "--porcelain", "--untracked-files=normal")
	if err != nil {
		return "unreadable"
	}
	n := 0
	for _, l := range lines(out) {
		if len(l) < 4 || setupFiles[strings.Trim(l[3:], `"`)] {
			continue
		}
		n++
	}
	if n == 0 {
		return ""
	}
	return strconv.Itoa(n) + " file(s)"
}

func loadPRs() map[string]*pr {
	m := map[string]*pr{}
	if _, err := exec.LookPath("gh"); err != nil {
		return m
	}
	out, err := exec.Command("gh", "pr", "list", "--state", "all", "--limit", "1000",
		"--json", "number,state,headRefName,headRefOid,url").Output()
	if err != nil {
		return m
	}
	var list []pr
	if json.Unmarshal(out, &list) != nil {
		return m
	}
	// gh lists newest first; keep the newest PR per branch.
	for i := range list {
		if _, ok := m[list[i].HeadRefName]; !ok {
			m[list[i].HeadRefName] = &list[i]
		}
	}
	return m
}

// statusText is one branch's status: upstream, distance to base, PR,
// worktree, age. With rich set it carries colours and OSC 8 links.
func statusText(b branch, r *repo, rich bool) string {
	c := func(code, s string) string {
		if !rich {
			return s
		}
		return sgr(code, s)
	}
	var st []string
	switch {
	case b.upstream == "":
		st = append(st, c("2", "no upstream"))
	case b.track == "[gone]":
		st = append(st, c("33", "remote gone"))
	case b.track == "":
		st = append(st, c("32", "synced"))
	default:
		st = append(st, c("33", strings.Trim(b.track, "[]")))
	}
	if ab, err := git("", "rev-list", "--left-right", "--count", r.baseRef+"..."+b.tip); err == nil {
		if f := strings.Fields(ab); len(f) == 2 {
			st = append(st, c("2", fmt.Sprintf("base -%s +%s", f[0], f[1])))
		}
	}
	if p := r.prs[b.name]; p != nil {
		s := fmt.Sprintf("PR #%d %s", p.Number, strings.ToLower(p.State))
		if p.State == "MERGED" && p.HeadRefOid != b.tip {
			s += " (new commits since)"
		}
		if rich {
			s = link(p.URL, s)
		}
		st = append(st, s)
	}
	if b.worktree != "" {
		wt := "wt"
		if d := dirty(b.worktree); d != "" {
			wt += " " + c("31", "dirty "+d)
		}
		if b.worktree == r.top {
			wt += " (here)"
		}
		if rich {
			wt = link("file://"+b.worktree, wt)
		}
		st = append(st, wt)
	}
	if rich {
		st = append(st, c("2", b.date))
	}
	sep := " · "
	if rich {
		sep = sgr("2", sep)
	}
	return strings.Join(st, sep)
}

// printLeft runs after the TUI closes, so the list stays in scrollback.
func printLeft(r *repo) {
	color = isTTY()
	fmt.Printf("%s %s\n", sgr("1", "repo"), link("file://"+r.top, r.top))
	if r.web != "" {
		fmt.Printf("%s %s\n", sgr("1", "web "), link(r.web, r.web))
	}
	bs := listBranches()
	sort.Slice(bs, func(i, j int) bool { return bs[i].name < bs[j].name })
	fmt.Println(sgr("1", fmt.Sprintf("\nleft in local (%d):", len(bs))))
	w := 0
	for _, b := range bs {
		w = max(w, len(b.name))
	}
	for _, b := range bs {
		fmt.Printf("  %-*s  %s\n", w, b.name, statusText(b, r, true))
	}
}

func repoURL() string {
	u, err := git("", "remote", "get-url", "origin")
	if err != nil {
		return ""
	}
	u = strings.TrimSuffix(u, ".git")
	if strings.HasPrefix(u, "git@") {
		u = "https://" + strings.Replace(strings.TrimPrefix(u, "git@"), ":", "/", 1)
	}
	if strings.HasPrefix(u, "http") {
		return u
	}
	return ""
}

func git(dir string, args ...string) (string, error) {
	if dir != "" {
		args = append([]string{"-C", dir}, args...)
	}
	cmd := exec.Command("git", args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if s := strings.TrimSpace(stderr.String()); s != "" {
			return "", fmt.Errorf("%s", s)
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func lines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, "gitprune:", msg)
	os.Exit(1)
}

var color = isTTY()

func isTTY() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	fi, err := os.Stdout.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func sgr(code, s string) string {
	if !color {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

// link wraps text in an OSC 8 hyperlink (clickable in iTerm2, Ghostty, kitty…).
func link(url, text string) string {
	if !color {
		return text
	}
	return "\x1b]8;;" + url + "\x1b\\" + text + "\x1b]8;;\x1b\\"
}
