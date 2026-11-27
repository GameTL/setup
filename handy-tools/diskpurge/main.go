package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
)

func main() {
	dryRun := flag.Bool("dry-run", false, "start in dry-run mode: show what would be purged, delete nothing")
	noSudo := flag.Bool("no-sudo", false, "don't ask for the sudo password up front; sudo is still asked for if a root-owned file turns up")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "purge - pick caches and build leftovers, then wipe them")
		fmt.Fprintln(os.Stderr)
		flag.PrintDefaults()
	}
	flag.Parse()

	home, err := os.UserHomeDir()
	if err != nil {
		fail(err)
	}

	groups := buildCatalog(home)
	if len(groups) == 0 {
		fmt.Println("=^･ｪ･^=  nothing to purge. already clean.")
		return
	}

	m := NewModel(groups, *dryRun)
	m.Sudo = !*noSudo && os.Geteuid() != 0 && onPath("sudo")
	if err := runTUI(m); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "purge:", err)
	os.Exit(1)
}

func onPath(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
