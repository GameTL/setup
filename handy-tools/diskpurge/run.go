package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

// ---- run events ---------------------------------------------------------
//
// The purge runs on its own goroutine so the screen keeps repainting. It
// never touches the Model: everything it has to say goes through runMsg, and
// the render loop applies those to the Model.

type runKind int

const (
	runStart runKind = iota // item idx began
	runOut                  // item idx printed data
	runAsk                  // item idx started or stopped waiting for a password
	runEnd                  // item idx finished, err says how
	runSkip                 // item idx was never started (run was stopped)
	runAll                  // every item is accounted for
)

type runMsg struct {
	kind runKind
	idx  int
	data []byte
	ask  bool
	err  error
}

// job is what one purge step gets to work with: a place to print, the keys
// the user types while it runs, and a signal that the run was stopped.
type job struct {
	emit   func([]byte)
	ask    func(waiting bool) // may be nil
	input  <-chan []byte
	cancel <-chan struct{}
	done   <-chan struct{} // closed once the whole run is over; may be nil
}

func (j *job) logf(format string, args ...any) {
	j.emit([]byte(fmt.Sprintf(format, args...) + "\n"))
}

func (j *job) cancelled() bool {
	select {
	case <-j.cancel:
		return true
	default:
		return false
	}
}

var errStopped = fmt.Errorf("stopped")

// runJobs purges items one after another and reports progress on ev.
func runJobs(items []*Item, dry bool, ev chan<- runMsg, input <-chan []byte, cancel <-chan struct{}) {
	done := make(chan struct{})
	defer close(done)
	for i, it := range items {
		j := &job{
			emit:   func(b []byte) { ev <- runMsg{kind: runOut, idx: i, data: b} },
			ask:    func(on bool) { ev <- runMsg{kind: runAsk, idx: i, ask: on} },
			input:  input,
			cancel: cancel,
			done:   done,
		}
		if j.cancelled() {
			ev <- runMsg{kind: runSkip, idx: i}
			continue
		}
		ev <- runMsg{kind: runStart, idx: i}
		var err error
		if !dry {
			err = it.purge(j)
		}
		ev <- runMsg{kind: runEnd, idx: i, err: err}
	}
	ev <- runMsg{kind: runAll}
}

// ---- commands -----------------------------------------------------------

// runCmd runs a command on its own pseudo-terminal, like `-it`: the command
// sees a real terminal, its output streams out as it is written, and keys the
// user types are passed in, so a prompt can be answered and ctrl-c reaches it.
func runCmd(j *job, name string, args ...string) error {
	if _, err := exec.LookPath(name); err != nil {
		return fmt.Errorf("%s not installed", name)
	}
	master, slave, err := openPTY(100, 30)
	if err != nil {
		return fmt.Errorf("no terminal for %s: %v", name, err)
	}
	defer func() { _ = master.Close() }()

	cmd := exec.Command(name, args...)
	cmd.Env = append(os.Environ(), "NO_COLOR=1")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	err = cmd.Start()
	_ = slave.Close()
	if err != nil {
		return fmt.Errorf("%s: %v", name, err)
	}

	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			select {
			case b := <-j.input:
				_, _ = master.Write(b)
			case <-stop:
				return
			}
		}
	}()

	// The read ends with an error once the command exits and lets go of the
	// terminal.
	return watch(j, cmd, name, master)
}

// runPiped runs a command with no terminal at all: pipes for its input and
// output, in a session of its own. Nothing a terminal would do happens, so it
// is done here: the keys typed are collected into a line, with backspace,
// until enter sends it, and stopping the run sends an interrupt.
func runPiped(j *job, name string, args ...string) error {
	if _, err := exec.LookPath(name); err != nil {
		return fmt.Errorf("%s not installed", name)
	}
	inR, inW, err := os.Pipe()
	if err != nil {
		return err
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		_ = inR.Close()
		_ = inW.Close()
		return err
	}
	defer func() { _ = inW.Close() }()
	defer func() { _ = outR.Close() }()

	cmd := exec.Command(name, args...)
	cmd.Env = append(os.Environ(), "NO_COLOR=1")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = inR, outW, outW
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	err = cmd.Start()
	_ = inR.Close()
	_ = outW.Close()
	if err != nil {
		return fmt.Errorf("%s: %v", name, err)
	}

	stop := make(chan struct{})
	defer close(stop)
	go func() {
		cancel := j.cancel
		var line []byte
		for {
			select {
			case b := <-j.input:
				for _, k := range splitKeys(string(b)) {
					switch {
					case k == "\r" || k == "\n":
						_, _ = inW.Write(append(line, '\n'))
						line = line[:0]
					case k == "\x7f" || k == "\b":
						if len(line) > 0 {
							_, size := utf8.DecodeLastRune(line)
							line = line[:len(line)-size]
						}
					case k == "\x15":
						line = line[:0]
					case k[0] >= 0x20:
						line = append(line, k...)
					}
				}
			case <-cancel:
				_ = cmd.Process.Signal(syscall.SIGINT)
				cancel = nil
			case <-stop:
				return
			}
		}
	}()

	// The read ends once the command exits and closes its output.
	return watch(j, cmd, name, outR)
}

// watch streams a started command's output until it ends, then waits for it.
// A private copy of the output is kept for the error message.
func watch(j *job, cmd *exec.Cmd, name string, out io.Reader) error {
	var seen termLog
	asking := false
	setAsking := func(on bool) {
		if on != asking && j.ask != nil {
			j.ask(on)
		}
		asking = on
	}
	defer setAsking(false)
	buf := make([]byte, 4096)
	for {
		n, rerr := out.Read(buf)
		if n > 0 {
			chunk := append([]byte(nil), buf[:n]...)
			if asking && strings.ContainsRune(string(chunk), '\n') {
				setAsking(false)
			}
			seen.Write(chunk)
			j.emit(chunk)
			// A line left open on a password prompt means the command is
			// waiting for one; the screen then shows a masked input field.
			setAsking(strings.HasSuffix(strings.ToLower(seen.Current()), passwordPrompt))
		}
		if rerr != nil {
			break
		}
	}

	if err := cmd.Wait(); err != nil {
		if j.cancelled() {
			return errStopped
		}
		if last := seen.Last(); last != "" {
			return fmt.Errorf("%s failed: %s", name, last)
		}
		return fmt.Errorf("%s failed: %v", name, err)
	}
	return nil
}

// passwordPrompt is how a line that waits for a password ends (lower case).
// sudo is told to use exactly this; other tools that prompt the same way get
// the masked field too.
const passwordPrompt = "password: "

// runSudo runs sudo with args. The password, if sudo wants one, is typed in
// the app.
//
// sudo runs with no terminal and reads the password from its stdin (-S).
// That is what lets one password last the whole run: sudo remembers a
// password per terminal session, and every command here gets a new one, but
// with no terminal it remembers it per parent process, and every sudo here is
// a child of this one.
func runSudo(j *job, args ...string) error {
	return runPiped(j, "sudo", append([]string{"-S", "-p", "Password: "}, args...)...)
}

// sudoAuth asks for the sudo password before anything is deleted, so a
// root-owned file found later is deleted without stopping the run to ask.
// While the run goes on it keeps sudo from forgetting the password.
func sudoAuth(j *job) error {
	if err := runSudo(j, "-v"); err != nil {
		return err
	}
	go keepSudo(j.done, j.cancel)
	return nil
}

// sudoRefresh is how often keepSudo renews the password. sudo forgets it 5
// minutes after the last use by default.
const sudoRefresh = 2 * time.Minute

func keepSudo(done, cancel <-chan struct{}) {
	t := time.NewTicker(sudoRefresh)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			cmd := exec.Command("sudo", "-n", "-v")
			cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
			_ = cmd.Run()
		case <-done:
			return
		case <-cancel:
			return
		}
	}
}

// sudoItem is the first step of every real run: sudoAuth, shown as a row.
func sudoItem() *Item {
	return &Item{Name: "sudo", Action: "sudo -v", Sizeless: true, purge: sudoAuth}
}

// sudoRemove deletes paths the user's own account may not, by asking sudo.
func sudoRemove(j *job, home string, paths []string) error {
	j.logf("%d item(s) belong to another user (root): deleting with sudo", len(paths))
	for _, p := range paths {
		if err := guard(p, home); err != nil {
			return err
		}
	}
	return runSudo(j, append([]string{"rm", "-rf", "--"}, paths...)...)
}

func ioctl(fd, req, arg uintptr) error {
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, req, arg); errno != 0 {
		return errno
	}
	return nil
}

// ---- deletes ------------------------------------------------------------

// removeTree deletes what is inside dir one entry at a time, printing each,
// so a big delete shows where it is and can be stopped between entries.
// With keepDir the directory itself stays (the Trash).
//
// Entries owned by another user are collected and deleted with sudo at the
// end. Entries macOS itself protects can't be deleted even by root; those are
// left and reported.
func removeTree(j *job, dir, home string, keepDir bool) error {
	if err := guard(dir, home); err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	switch {
	case os.IsNotExist(err):
		return nil
	case err != nil && keepDir:
		return err
	case errors.Is(err, syscall.EACCES):
		return sudoRemove(j, home, []string{dir})
	case err != nil:
		// Not a directory we can list: remove it whole.
		j.logf("rm -rf %s", tilde(dir, home))
		return removeAll(dir)
	}

	var denied []string
	var firstErr error
	protected := 0
	for _, e := range entries {
		if j.cancelled() {
			return errStopped
		}
		child := filepath.Join(dir, e.Name())
		j.logf("rm -rf %s", tilde(child, home))
		err := removeAll(child)
		switch {
		case err == nil:
		case errors.Is(err, syscall.EACCES):
			denied = append(denied, child)
		case errors.Is(err, syscall.EPERM):
			protected++
			j.logf("  protected by macOS, left alone")
		case firstErr == nil:
			firstErr = err
		}
	}
	if len(denied) > 0 {
		if err := sudoRemove(j, home, denied); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	switch {
	case firstErr != nil:
		return firstErr
	case protected > 0:
		return fmt.Errorf("%d item(s) are protected by macOS and were left", protected)
	case keepDir:
		return nil
	}
	// Not a plain rmdir: Finder may have dropped a .DS_Store in here since
	// the entries were listed.
	return removeAll(dir)
}

// removeAll is os.RemoveAll that tries again when the folder turns out not
// to be empty. That happens when something writes into it mid-delete, most
// often Finder adding a .DS_Store to a folder it is showing.
func removeAll(path string) error {
	var err error
	for try := 0; try < 5; try++ {
		if err = os.RemoveAll(path); !errors.Is(err, syscall.ENOTEMPTY) {
			return err
		}
		time.Sleep(50 * time.Millisecond)
	}
	return err
}

// ---- output log ---------------------------------------------------------

// termLog turns raw terminal output into plain lines. Colour escapes are
// dropped. A carriage return restarts the current line, and so do the
// "cursor to column 1" and "erase line" escapes: between them that is how
// progress bars and spinners redraw themselves.
type termLog struct {
	lines [][]byte // finished lines
	cur   []byte   // line being written
	cr    bool     // the cursor is back at the start: the next character restarts the line
	esc   int      // escape parser state, kept across writes
	csi   []byte   // parameters of the escape being read
}

const (
	escNone = iota
	escStart
	escCSI
	escOSC
	escOSCEnd
)

const maxLogLines = 500

func (t *termLog) Write(p []byte) {
	for _, b := range p {
		switch t.esc {
		case escStart:
			switch b {
			case '[':
				t.esc, t.csi = escCSI, t.csi[:0]
			case ']':
				t.esc = escOSC
			default:
				t.esc = escNone
			}
			continue
		case escCSI:
			if b < 0x40 || b > 0x7e {
				t.csi = append(t.csi, b)
				continue
			}
			t.esc = escNone
			switch param := string(t.csi); {
			case b == 'G' && (param == "" || param == "1"):
				t.cr = true // cursor to column 1
			case b == 'K' && param == "2", b == 'K' && t.cr:
				t.cur, t.cr = nil, false // erase the line (from column 1)
			}
			continue
		case escOSC:
			switch b {
			case 0x07:
				t.esc = escNone
			case 0x1b:
				t.esc = escOSCEnd
			}
			continue
		case escOSCEnd:
			t.esc = escNone
			continue
		}

		switch {
		case b == 0x1b:
			t.esc = escStart
		case b == '\n':
			t.lines = append(t.lines, t.cur)
			if len(t.lines) > maxLogLines {
				t.lines = t.lines[len(t.lines)-maxLogLines:]
			}
			t.cur, t.cr = nil, false
		case b == '\r':
			t.cr = true
		case b == '\b':
			if len(t.cur) > 0 {
				t.cur = t.cur[:len(t.cur)-1]
			}
		case b == '\t':
			t.put(' ')
		case b < 0x20 || b == 0x7f:
			// other control bytes carry nothing to show
		default:
			t.put(b)
		}
	}
}

func (t *termLog) put(b byte) {
	if t.cr {
		t.cur, t.cr = nil, false
	}
	t.cur = append(t.cur, b)
}

// Current returns the line being written, before any newline ends it.
func (t *termLog) Current() string {
	if t.cr {
		return ""
	}
	return string(t.cur)
}

// Tail returns the last n non-empty lines, oldest first.
func (t *termLog) Tail(n int) []string {
	var out []string
	add := func(b []byte) {
		if s := strings.TrimRight(string(b), " "); strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	for _, l := range t.lines {
		add(l)
	}
	add(t.cur)
	if len(out) > n {
		out = out[len(out)-n:]
	}
	return out
}

// Last returns the last non-empty line, or "".
func (t *termLog) Last() string {
	if tail := t.Tail(1); len(tail) == 1 {
		return strings.TrimSpace(tail[0])
	}
	return ""
}
