package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// collect runs fn as one job and returns everything it printed.
func collect(t *testing.T, input chan []byte, cancel chan struct{}, fn func(*job) error) (string, error) {
	t.Helper()
	var log termLog
	j := &job{emit: func(b []byte) { log.Write(b) }, input: input, cancel: cancel}
	err := fn(j)
	return strings.Join(log.Tail(100), "\n"), err
}

func TestRunCmdGivesTheCommandATerminal(t *testing.T) {
	out, err := collect(t, make(chan []byte), make(chan struct{}), func(j *job) error {
		return runCmd(j, "sh", "-c", "test -t 0 && test -t 1 && echo is-a-tty; echo second")
	})
	if err != nil {
		t.Fatal(err)
	}
	if out != "is-a-tty\nsecond" {
		t.Fatalf("output = %q", out)
	}
}

func TestRunCmdPassesTypedKeys(t *testing.T) {
	input := make(chan []byte)
	go func() { input <- []byte("yes\r") }()
	out, err := collect(t, input, make(chan struct{}), func(j *job) error {
		return runCmd(j, "sh", "-c", "printf 'sure? '; read a; echo got=$a")
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "got=yes") {
		t.Fatalf("output = %q", out)
	}
}

func TestRunCmdFailureReportsLastLine(t *testing.T) {
	_, err := collect(t, make(chan []byte), make(chan struct{}), func(j *job) error {
		return runCmd(j, "sh", "-c", "echo first; echo boom; exit 3")
	})
	if err == nil || err.Error() != "sh failed: boom" {
		t.Fatalf("err = %v", err)
	}
}

func TestRunCmdCtrlCStopsTheCommand(t *testing.T) {
	input, cancel := make(chan []byte), make(chan struct{})
	go func() {
		time.Sleep(300 * time.Millisecond)
		close(cancel)
		input <- []byte("\x03")
	}()
	start := time.Now()
	_, err := collect(t, input, cancel, func(j *job) error { return runCmd(j, "sleep", "30") })
	if err != errStopped {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("ctrl-c did not interrupt the command")
	}
}

// sudo must find no controlling terminal, or it remembers the password only
// for that one terminal, which no later command shares.
func TestRunPipedHasNoTerminal(t *testing.T) {
	out, err := collect(t, make(chan []byte), make(chan struct{}), func(j *job) error {
		return runPiped(j, "sh", "-c", "ps -o tty= -p $$")
	})
	if err != nil {
		t.Fatal(err)
	}
	if out != "??" {
		t.Fatalf("output = %q", out)
	}
}

func TestRunPipedEditsTheLine(t *testing.T) {
	input := make(chan []byte)
	go func() {
		input <- []byte("ab\x7fc")
		input <- []byte("\x1b[Adé\x7f\x7fd\r")
	}()
	out, err := collect(t, input, make(chan struct{}), func(j *job) error {
		return runPiped(j, "sh", "-c", "read a; echo got=$a")
	})
	if err != nil {
		t.Fatal(err)
	}
	if out != "got=acd" {
		t.Fatalf("output = %q", out)
	}
}

func TestRunPipedStops(t *testing.T) {
	input, cancel := make(chan []byte), make(chan struct{})
	go func() {
		time.Sleep(300 * time.Millisecond)
		close(cancel)
	}()
	start := time.Now()
	_, err := collect(t, input, cancel, func(j *job) error { return runPiped(j, "sleep", "30") })
	if err != errStopped {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("stopping did not interrupt the command")
	}
}

// The password asked for at the start covers a root-owned file found later.
func TestSudoAuthUpFront(t *testing.T) {
	fakeSudo(t)
	home, dir := lockedTree(t)
	input := make(chan []byte)
	asks := 0
	var log termLog
	j := &job{
		emit:   func(b []byte) { log.Write(b) },
		input:  input,
		cancel: make(chan struct{}),
		ask: func(on bool) {
			if on {
				asks++
				go func() { input <- []byte("secret\r") }()
			}
		},
	}
	if err := sudoAuth(j); err != nil {
		t.Fatalf("auth: %v\n%s", err, strings.Join(log.Tail(50), "\n"))
	}
	if err := removeTree(j, dir, home, true); err != nil {
		t.Fatalf("err = %v\n%s", err, strings.Join(log.Tail(50), "\n"))
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("left behind: %v", entries)
	}
	if asks != 1 {
		t.Fatalf("asked for the password %d times, want 1", asks)
	}
}

func TestBeginStartsWithSudo(t *testing.T) {
	g := &Group{Name: "g", Items: []*Item{{Name: "a", Selected: true}}}
	m := NewModel([]*Group{g}, false)
	m.Sudo = true
	if got := m.Begin(); len(got) != 2 || got[0].Name != "sudo" || got[1].Name != "a" {
		t.Fatalf("run = %v", got)
	}
	m.DryRun = true
	if got := m.Begin(); len(got) != 1 {
		t.Fatalf("a dry run must not ask for sudo, run = %v", got)
	}
}

func TestRemoveTree(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "cache")
	for _, f := range []string{"a/x", "b/y/z"} {
		p := filepath.Join(dir, f)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		_ = os.WriteFile(p, []byte("hi"), 0o644)
	}

	out, err := collect(t, nil, make(chan struct{}), func(j *job) error { return removeTree(j, dir, home, true) })
	if err != nil {
		t.Fatal(err)
	}
	if out != "rm -rf ~/cache/a\nrm -rf ~/cache/b" {
		t.Fatalf("output = %q", out)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Fatalf("keepDir should leave an empty dir, got %v %v", entries, err)
	}

	if _, err := collect(t, nil, make(chan struct{}), func(j *job) error { return removeTree(j, dir, home, false) }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("dir should be gone, stat err = %v", err)
	}
	if err := removeTree(&job{}, home, home, false); err == nil {
		t.Fatal("removing the home dir must be refused")
	}
}

func TestTermLog(t *testing.T) {
	var l termLog
	l.Write([]byte("plain\r\n\x1b[32mgreen\x1b[0m\r\n10%\r50%\r100%\r\n\x1b]0;title\x07tail \x1b"))
	l.Write([]byte("[1mbold")) // escape split across two writes
	want := []string{"plain", "green", "100%", "tail bold"}
	if got := l.Tail(10); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q want %q", got, want)
	}
	if l.Last() != "tail bold" {
		t.Fatalf("last = %q", l.Last())
	}

	// npm draws a spinner, then wipes it with "column 1" + "erase line".
	var npm termLog
	npm.Write([]byte("npm error first\r\n\xe2\xa0\x99\x1b[1G\x1b[0Knpm error second\r\n\xe2\xa0\x99\x1b[1G\x1b[0K"))
	if got, want := npm.Tail(10), []string{"npm error first", "npm error second"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q want %q", got, want)
	}
}

// A folder that gets a new file while it is being deleted (Finder's
// .DS_Store) must still end up gone.
func TestRemoveAllRetriesWhenRefilled(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "debug")
	_ = os.MkdirAll(filepath.Join(dir, "deps"), 0o755)
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 40; i++ { // keep dropping a file in for ~80ms
			select {
			case <-stop:
				return
			default:
			}
			_ = os.WriteFile(filepath.Join(dir, ".DS_Store"), []byte("x"), 0o644)
			time.Sleep(2 * time.Millisecond)
		}
	}()
	time.Sleep(10 * time.Millisecond)
	err := removeAll(dir)
	close(stop)
	<-done
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("dir should be gone, stat err = %v", err)
	}
}

func TestSplitKeys(t *testing.T) {
	got := splitKeys("jj\x1b[B\x1bOAk\x1b")
	want := []string{"j", "j", "\x1b[B", "\x1b[A", "k", "\x1b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q want %q", got, want)
	}
}

// fakeSudo puts a stand-in sudo first on PATH. Like the real one run with
// -S, it prompts on stderr and reads the password from stdin. It accepts the
// password "secret" and remembers it, so -n and later calls don't ask again.
// It then makes the targets deletable before running the command, which is
// the part real sudo does by being root.
func fakeSudo(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	script := `#!/bin/sh
stamp="$(dirname "$0")/authed"
prompt="Password:"; ask=1; validate=0
while :; do
  case "$1" in
    -S) shift ;;
    -n) ask=0; shift ;;
    -v) validate=1; shift ;;
    -p) prompt="$2"; shift 2 ;;
    *) break ;;
  esac
done
if [ ! -e "$stamp" ]; then
  [ $ask = 0 ] && { echo "sudo: a password is required"; exit 1; }
  tries=0
  while :; do
    printf '%s' "$prompt" >&2; read pw; echo
    [ "$pw" = "secret" ] && break
    tries=$((tries+1))
    [ $tries -ge 2 ] && { echo "sudo: 2 incorrect password attempts"; exit 1; }
    echo "Sorry, try again."
  done
  touch "$stamp"
fi
[ $validate = 1 ] && exit 0
for a in "$@"; do [ -e "$a" ] && chmod -R u+w "$a"; done
exec "$@"
`
	if err := os.WriteFile(filepath.Join(bin, "sudo"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// lockedTree makes home/trash with one normal entry and one the user can't
// delete from, the way a root-owned folder in the Trash behaves.
func lockedTree(t *testing.T) (home, dir string) {
	t.Helper()
	home = t.TempDir()
	dir = filepath.Join(home, "trash")
	locked := filepath.Join(dir, "locked")
	_ = os.MkdirAll(filepath.Join(dir, "plain"), 0o755)
	_ = os.MkdirAll(locked, 0o755)
	_ = os.WriteFile(filepath.Join(locked, "version.json"), []byte("{}"), 0o644)
	_ = os.Chmod(locked, 0o555)
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	return home, dir
}

func TestRemoveTreeFallsBackToSudo(t *testing.T) {
	fakeSudo(t)
	home, dir := lockedTree(t)

	input := make(chan []byte)
	var asks []bool
	var log termLog
	j := &job{
		emit:   func(b []byte) { log.Write(b) },
		input:  input,
		cancel: make(chan struct{}),
		ask: func(on bool) {
			asks = append(asks, on)
			if on && len(asks) == 1 {
				go func() { input <- []byte("wrong\r") }()
			} else if on {
				go func() { input <- []byte("secret\r") }()
			}
		},
	}
	if err := removeTree(j, dir, home, true); err != nil {
		t.Fatalf("err = %v\n%s", err, strings.Join(log.Tail(50), "\n"))
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("left behind: %v", entries)
	}
	out := strings.Join(log.Tail(50), "\n")
	if strings.Contains(out, "secret") || strings.Contains(out, "wrong") {
		t.Fatalf("typed password showed up in the output:\n%s", out)
	}
	if !strings.Contains(out, "Sorry, try again.") {
		t.Fatalf("no retry prompt in:\n%s", out)
	}
	// asked, answered wrong, asked again, answered, done
	if want := []bool{true, false, true, false}; !reflect.DeepEqual(asks, want) {
		t.Fatalf("asks = %v want %v", asks, want)
	}
}

func TestRemoveTreeSudoRefused(t *testing.T) {
	fakeSudo(t)
	home, dir := lockedTree(t)
	input := make(chan []byte)
	var log termLog
	j := &job{
		emit:   func(b []byte) { log.Write(b) },
		input:  input,
		cancel: make(chan struct{}),
		ask: func(on bool) {
			if on {
				go func() { input <- []byte("nope\r") }()
			}
		},
	}
	err := removeTree(j, dir, home, true)
	if err == nil || err.Error() != "sudo failed: sudo: 2 incorrect password attempts" {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "locked", "version.json")); err != nil {
		t.Fatal("locked file should still be there")
	}
}

func TestNoteTyped(t *testing.T) {
	m := &Model{Results: []*Result{{Status: statusRunning}}}
	m.noteTyped([]string{"a", "b"})
	if m.Typed != 0 {
		t.Fatal("keys must not count when nothing is asking")
	}
	m.Results[0].Asking = true
	m.noteTyped([]string{"a", "b", "c", "\x7f", "\x1b[A"})
	if m.Typed != 2 {
		t.Fatalf("typed = %d", m.Typed)
	}
	m.noteTyped([]string{"\r"})
	if m.Typed != 0 {
		t.Fatalf("enter should clear the field, typed = %d", m.Typed)
	}
}

func TestLibraryCaches(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "Library", "Caches")
	write := func(name string, size int) {
		_ = os.MkdirAll(filepath.Join(root, name), 0o755)
		f, _ := os.Create(filepath.Join(root, name, "blob"))
		_ = f.Truncate(int64(size))
		_ = f.Close()
	}
	write("small", 1<<20)
	write("big", 30<<20)
	write("bigger", 50<<20)
	write("go-build", 90<<20)
	write("com.apple.thing", 25<<20)

	items := libraryCaches(home, filepath.Join(root, "go-build"), "")
	var got []string
	for _, it := range items {
		if it.Selected || !it.Scanned {
			t.Fatalf("%s: should be measured and not pre-selected", it.Name)
		}
		got = append(got, it.Name+" | "+it.Note+" | "+it.Action)
	}
	want := []string{
		"bigger | app cache, rebuilt on use | rm -rf ~/Library/Caches/bigger",
		"big | app cache, rebuilt on use | rm -rf ~/Library/Caches/big",
		"com.apple.thing | macOS system cache | rm -rf ~/Library/Caches/com.apple.thing",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

func TestAppCaches(t *testing.T) {
	home := t.TempDir()
	lib := filepath.Join(home, "Library")
	write := func(dir string, size int) {
		full := filepath.Join(lib, dir)
		_ = os.MkdirAll(full, 0o755)
		f, _ := os.Create(filepath.Join(full, "blob"))
		_ = f.Truncate(int64(size))
		_ = f.Close()
	}
	write("Application Support/Cursor/CachedData", 40<<20)
	write("Application Support/discord/Cache", 30<<20)
	write("Application Support/Code/User/globalStorage", 90<<20) // not a cache
	write("Application Support/Code/Cache", 1<<20)               // too small
	write("Containers/com.example.app/Data/Library/Caches", 60<<20)

	var got []string
	for _, it := range appCaches(home) {
		if it.Selected || !it.Scanned {
			t.Fatalf("%s: should be measured and not pre-selected", it.Name)
		}
		got = append(got, it.Name)
	}
	want := []string{
		"Containers/com.example.app/Data/Library/Caches",
		"Cursor/CachedData",
		"discord/Cache",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

func TestToggleExpandAll(t *testing.T) {
	a := &Group{Name: "a", Items: []*Item{{Name: "a1"}, {Name: "a2"}}}
	b := &Group{Name: "b", Items: []*Item{{Name: "b1"}}}
	m := NewModel([]*Group{a, b}, false)
	if !m.allExpanded() || len(m.rows()) != 5 {
		t.Fatalf("should start fully open, rows = %d", len(m.rows()))
	}

	m.Cursor = 4 // b1
	m.toggleExpandAll()
	if m.allExpanded() || len(m.rows()) != 2 {
		t.Fatalf("should be fully closed, rows = %d", len(m.rows()))
	}
	if r, _ := m.current(); r.group != b || r.item != nil {
		t.Fatalf("cursor should land on b1's group, got %+v at %d", r, m.Cursor)
	}

	a.Expanded = true // one open, one closed: the key opens the rest
	m.Cursor = 3      // group b, now below a's two items
	m.toggleExpandAll()
	if !m.allExpanded() {
		t.Fatal("a mixed tree should open fully")
	}
	if r, _ := m.current(); r.group != b || r.item != nil {
		t.Fatalf("cursor should stay on group b, got %+v at %d", r, m.Cursor)
	}
}

func TestDiscoverRustDebug(t *testing.T) {
	home := t.TempDir()
	mkCargo := func(rel string, hasDebug bool) string {
		proj := filepath.Join(home, rel)
		_ = os.MkdirAll(filepath.Join(proj, "target"), 0o755)
		_ = os.WriteFile(filepath.Join(proj, "Cargo.toml"), []byte("[package]\n"), 0o644)
		if hasDebug {
			dbg := filepath.Join(proj, "target", "debug")
			_ = os.MkdirAll(dbg, 0o755)
			return dbg
		}
		_ = os.MkdirAll(filepath.Join(proj, "target", "release"), 0o755)
		return ""
	}

	// Should find: standard repo, hidden worktree dirs, other top-level folders,
	// nested hidden worktrees, and standalone Cargo target dirs with CACHEDIR.TAG.
	want := []string{
		mkCargo(".t3/worktrees/any-repo/t3-abc123", true),
		mkCargo("Documents/rust-proj", true),
		mkCargo("GLab/team/service", true),
		mkCargo("Github/app/.claude/worktrees/feat-1", true),
		mkCargo("Github/app", true),
	}
	tagTarget := filepath.Join(home, "custom", "target")
	_ = os.MkdirAll(filepath.Join(tagTarget, "debug"), 0o755)
	_ = os.WriteFile(filepath.Join(tagTarget, "CACHEDIR.TAG"),
		[]byte("Signature: 8a477f597d28d172789f06886806bc55\n# created by cargo.\n"), 0o644)
	want = append(want, filepath.Join(tagTarget, "debug"))

	// Should ignore: release-only, non-Rust target/debug, skipped roots/dirs,
	// macOS bundles, and paths deeper than maxDepth.
	mkCargo("Github/release-only", false)
	_ = os.MkdirAll(filepath.Join(home, "Github/not-rust/target/debug"), 0o755)
	mkCargo("Library/Caches/some-rust", true)
	mkCargo(".Trash/deleted-rust", true)
	mkCargo(".cargo/registry/src/crate", true)
	mkCargo("Github/web/node_modules/pkg", true)
	mkCargo("Github/app/.git/sub", true)
	mkCargo("Desktop/MyApp.app/Contents/proj", true)
	mkCargo("a/b/c/d/e/f/g/too-deep", true)

	got := discoverRustDebug(home, 7)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}
