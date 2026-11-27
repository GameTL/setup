package main

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// buildCatalog lists everything purge knows how to clean. Items whose path
// does not exist on this machine are dropped, so empty groups disappear.
func buildCatalog(home string) []*Group {
	var groups []*Group
	add := func(name string, items ...*Item) {
		var keep []*Item
		for _, it := range items {
			if it.Path == "" {
				continue
			}
			if _, err := os.Stat(it.Path); err != nil {
				continue
			}
			keep = append(keep, it)
		}
		if len(keep) > 0 {
			groups = append(groups, &Group{Name: name, Items: keep})
		}
	}

	add("Trash",
		newItem("Trash", filepath.Join(home, ".Trash"), "deleted for good", "rm -rf ~/.Trash/*", true,
			func(j *job) error { return removeTree(j, filepath.Join(home, ".Trash"), home, true) }),
	)

	goCache := goEnv("GOCACHE")
	add("Go",
		newItem("build cache", goCache, "compiled objects", "go clean -cache", true,
			cmdFn("go", "clean", "-cache")),
		newItem("module cache", goEnv("GOMODCACHE"), "re-downloads on build", "go clean -modcache", false,
			cmdFn("go", "clean", "-modcache")),
	)

	cargoSrc := filepath.Join(home, ".cargo", "registry", "src")
	add("Rust",
		newItem("cargo src", cargoSrc, "re-extracted on build", "rm -rf "+tilde(cargoSrc, home), true,
			removeFn(cargoSrc, home)),
	)

	var debugs []*Item
	seenDebug := map[string]bool{}
	addDebug := func(dbg string) {
		dbg = filepath.Clean(dbg)
		if seenDebug[dbg] {
			return
		}
		seenDebug[dbg] = true
		labelDir := filepath.Dir(dbg)
		if filepath.Base(labelDir) == "target" {
			labelDir = filepath.Dir(labelDir)
		}
		debugs = append(debugs, newItem(
			filepath.Base(labelDir),
			dbg,
			tilde(labelDir, home),
			"rm -rf "+tilde(dbg, home),
			true,
			removeFn(dbg, home),
		))
	}
	for _, dbg := range discoverRustDebug(home, 7) {
		addDebug(dbg)
	}
	if targetDir := os.Getenv("CARGO_TARGET_DIR"); targetDir != "" {
		dbg := filepath.Join(targetDir, "debug")
		if isCargoTarget(targetDir) {
			if st, err := os.Stat(dbg); err == nil && st.IsDir() {
				addDebug(dbg)
			}
		}
	}
	add("Rust debug", debugs...)

	npm := filepath.Join(home, ".npm")
	add("Node",
		// Deleted directly rather than with `npm cache clean`: npm gives up on
		// root-owned files that old npm versions left in its cache, while a
		// direct delete can fall back to sudo for them.
		newItem("npm cache", filepath.Join(npm, "_cacache"), "downloaded packages", "rm -rf ~/.npm/_cacache", true,
			removeFn(filepath.Join(npm, "_cacache"), home)),
		newItem("npx cache", filepath.Join(npm, "_npx"), "npx installs, breaks running npx tools", "rm -rf ~/.npm/_npx", false,
			removeFn(filepath.Join(npm, "_npx"), home)),
	)

	brewCache := cmdOutput("brew", "--cache")
	hermesCache := filepath.Join(home, ".hermes", "cache")
	add("Caches",
		newItem("homebrew", brewCache, "downloaded bottles", "brew cleanup --prune=all -s", true,
			cmdFn("brew", "cleanup", "--prune=all", "-s")),
		newItem("uv", filepath.Join(home, ".cache", "uv"), "wheel + git cache", "uv cache clean", true,
			cmdFn("uv", "cache", "clean")),
		newItem("hermes", hermesCache, "pnpm + uv downloads", "rm -rf ~/.hermes/cache/*", true,
			func(j *job) error { return removeTree(j, hermesCache, home, true) }),
	)

	// Everything else in ~/Library/Caches, one row per app. The Go and
	// Homebrew caches live there too but are cleaned by their own tools above.
	add("Library caches", libraryCaches(home, goCache, brewCache)...)

	// Caches kept outside ~/Library/Caches: Electron and Chromium apps under
	// Application Support, and sandboxed apps under Containers.
	add("App caches", appCaches(home)...)

	return groups
}

func newItem(name, path, note, action string, selected bool, fn func(*job) error) *Item {
	return &Item{Name: name, Path: path, Note: note, Action: action, Selected: selected, purge: fn}
}

// minLibraryCache is the smallest ~/Library/Caches folder worth its own row.
const minLibraryCache = 20 << 20

// libraryCaches lists the folders in ~/Library/Caches that hold a meaningful
// amount of data, biggest first. None are pre-selected: an app rebuilds its
// cache, but may be slower or re-download things afterwards. Paths in skip
// are handled elsewhere.
func libraryCaches(home string, skip ...string) []*Item {
	root := filepath.Join(home, "Library", "Caches")
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	skipped := map[string]bool{}
	for _, p := range skip {
		if p != "" {
			skipped[filepath.Clean(p)] = true
		}
	}

	// Sizes are needed up front to decide which folders get a row, so they
	// are measured here, all at once, rather than later by the screen.
	sizes := make([]int64, len(entries))
	var wg sync.WaitGroup
	for i, e := range entries {
		path := filepath.Join(root, e.Name())
		if !e.IsDir() || skipped[path] {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			sizes[i] = dirSize(path)
		}()
	}
	wg.Wait()

	notes := map[string]string{
		"tidal-rust":    "app audio, re-downloads",
		"ms-playwright": "browser binaries, re-downloads",
	}
	var items []*Item
	for i, e := range entries {
		if sizes[i] < minLibraryCache {
			continue
		}
		name := e.Name()
		path := filepath.Join(root, name)
		note, ok := notes[name]
		switch {
		case ok:
		case strings.HasPrefix(name, "com.apple."):
			note = "macOS system cache"
		default:
			note = "app cache, rebuilt on use"
		}
		it := newItem(name, path, note, "rm -rf "+tilde(path, home), false, removeFn(path, home))
		it.Size, it.Scanned = sizes[i], true
		items = append(items, it)
	}
	sort.SliceStable(items, func(a, b int) bool { return items[a].Size > items[b].Size })
	return items
}

// appCacheNames are folder names that Electron, Chromium and VS Code based apps
// use for data they can rebuild. Matching is by exact name.
var appCacheNames = map[string]bool{
	"Cache": true, "Caches": true, "Code Cache": true, "GPUCache": true,
	"CachedData": true, "CachedExtensionVSIXs": true, "DownloadCache": true,
	"GraphiteDawnCache": true, "DawnGraphiteCache": true, "DawnWebGPUCache": true,
	"component_crx_cache": true, "CacheStorage": true, "appcache": true,
}

// appCaches lists rebuildable cache folders inside ~/Library/Application
// Support/<app> (up to two levels down) and ~/Library/Containers/<app>/Data/
// Library/Caches, biggest first. None are pre-selected.
func appCaches(home string) []*Item {
	lib := filepath.Join(home, "Library")
	var paths []string

	support := filepath.Join(lib, "Application Support")
	apps, _ := os.ReadDir(support)
	for _, app := range apps {
		if !app.IsDir() {
			continue
		}
		appDir := filepath.Join(support, app.Name())
		subs, _ := os.ReadDir(appDir)
		for _, sub := range subs {
			if !sub.IsDir() {
				continue
			}
			subDir := filepath.Join(appDir, sub.Name())
			if appCacheNames[sub.Name()] {
				paths = append(paths, subDir)
				continue
			}
			inner, _ := os.ReadDir(subDir)
			for _, in := range inner {
				if in.IsDir() && appCacheNames[in.Name()] {
					paths = append(paths, filepath.Join(subDir, in.Name()))
				}
			}
		}
	}

	containers := filepath.Join(lib, "Containers")
	boxes, _ := os.ReadDir(containers)
	for _, box := range boxes {
		paths = append(paths, filepath.Join(containers, box.Name(), "Data", "Library", "Caches"))
	}

	sizes := make([]int64, len(paths))
	var wg sync.WaitGroup
	for i, path := range paths {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sizes[i] = dirSize(path)
		}()
	}
	wg.Wait()

	var items []*Item
	for i, path := range paths {
		if sizes[i] < minLibraryCache {
			continue
		}
		name := strings.TrimPrefix(path, lib+string(filepath.Separator))
		name = strings.TrimPrefix(name, "Application Support"+string(filepath.Separator))
		it := newItem(name, path, "app cache, rebuilt on use", "rm -rf "+tilde(path, home), false, removeFn(path, home))
		it.Size, it.Scanned = sizes[i], true
		items = append(items, it)
	}
	sort.SliceStable(items, func(a, b int) bool { return items[a].Size > items[b].Size })
	return items
}

// rustSkipRoot are top-level folders under $HOME that never hold Rust
// workspaces or that macOS protects or syncs from the cloud.
var rustSkipRoot = map[string]bool{
	"Library": true, "Applications": true, "Movies": true,
	"Music": true, "Pictures": true, "Public": true,
	"OrbStack": true, "Parallels": true,
	".Trash": true, ".cargo": true, ".rustup": true,
	".npm": true, ".bun": true, ".cache": true, ".oh-my-zsh": true,
}

// rustSkipDir are folder names skipped at any depth while searching for
// Rust target/debug caches.
var rustSkipDir = map[string]bool{
	".git": true, ".hg": true, ".svn": true,
	"node_modules": true, ".venv": true, "venv": true,
	"__pycache__": true, ".tox": true, ".mypy_cache": true, ".pytest_cache": true,
	"Pods": true, ".gradle": true, ".m2": true,
	".next": true, ".nuxt": true, ".astro": true, "dist": true,
}

func isMacBundle(name string) bool {
	for _, ext := range []string{".app", ".framework", ".photoslibrary", ".xcodeproj", ".xcworkspace", ".bundle"} {
		if strings.HasSuffix(name, ext) {
			return true
		}
	}
	return false
}

// isCargoTarget reports whether targetDir looks like a Cargo target directory.
func isCargoTarget(targetDir string) bool {
	if _, err := os.Stat(filepath.Join(filepath.Dir(targetDir), "Cargo.toml")); err == nil {
		return true
	}
	if _, err := os.Stat(filepath.Join(targetDir, ".rustc_info.json")); err == nil {
		return true
	}
	if b, err := os.ReadFile(filepath.Join(targetDir, "CACHEDIR.TAG")); err == nil {
		return strings.Contains(strings.ToLower(string(b)), "cargo")
	}
	return false
}

// discoverRustDebug finds Cargo <project>/target/debug directories anywhere
// under root up to maxDepth levels deep, including hidden directories such as
// worktree folders (~/.t3, ~/.cursor, <repo>/.claude/worktrees). Top-level
// directories are walked concurrently.
func discoverRustDebug(root string, maxDepth int) []string {
	root = filepath.Clean(root)
	if maxDepth < 1 {
		return nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}

	results := make([][]string, len(entries))
	var wg sync.WaitGroup
	for i, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if rustSkipRoot[name] || rustSkipDir[name] || isMacBundle(name) {
			continue
		}
		sub := filepath.Join(root, name)
		if name == "target" && isCargoTarget(sub) {
			dbg := filepath.Join(sub, "debug")
			if st, err := os.Stat(dbg); err == nil && st.IsDir() {
				results[i] = []string{dbg}
			}
			continue
		}
		if maxDepth == 1 {
			continue
		}
		wg.Add(1)
		go func(idx int, dir string) {
			defer wg.Done()
			var local []string
			_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
				if err != nil || !d.IsDir() {
					return nil
				}
				if p == dir {
					return nil
				}
				rel, _ := filepath.Rel(dir, p)
				depth := strings.Count(rel, string(filepath.Separator)) + 2
				if depth > maxDepth {
					return filepath.SkipDir
				}
				n := d.Name()
				if rustSkipDir[n] || isMacBundle(n) {
					return filepath.SkipDir
				}
				if n == "target" && isCargoTarget(p) {
					dbg := filepath.Join(p, "debug")
					if st, err := os.Stat(dbg); err == nil && st.IsDir() {
						local = append(local, dbg)
					}
					return filepath.SkipDir
				}
				if depth == maxDepth {
					return filepath.SkipDir
				}
				return nil
			})
			results[idx] = local
		}(i, sub)
	}
	wg.Wait()

	var found []string
	for _, r := range results {
		found = append(found, r...)
	}
	sort.Strings(found)
	return found
}

// guard refuses to delete anything that is not a plain subpath of the machine.
func guard(path, home string) error {
	clean := filepath.Clean(path)
	if !filepath.IsAbs(clean) || clean == "/" || clean == filepath.Clean(home) {
		return fmt.Errorf("refusing to delete %q", path)
	}
	return nil
}

func removeFn(path, home string) func(*job) error {
	return func(j *job) error { return removeTree(j, path, home, false) }
}

func cmdFn(name string, args ...string) func(*job) error {
	return func(j *job) error { return runCmd(j, name, args...) }
}

func cmdOutput(name string, args ...string) string {
	if _, err := exec.LookPath(name); err != nil {
		return ""
	}
	out, err := exec.Command(name, args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func goEnv(key string) string {
	return cmdOutput("go", "env", key)
}

func tilde(p, home string) string {
	if strings.HasPrefix(p, home+string(filepath.Separator)) {
		return "~" + p[len(home):]
	}
	return p
}

// dirSize sums regular file sizes under path without following symlinks.
func dirSize(path string) int64 {
	var total int64
	_ = filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}
