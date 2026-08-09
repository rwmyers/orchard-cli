package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// tempRepo creates an empty git repository and returns its path with symlinks
// resolved, so it compares equal to what `git rev-parse --show-toplevel`
// reports for it.
func tempRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "--quiet", dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks(%s): %v", dir, err)
	}
	return resolved
}

// isolate points HOME and the working directory at empty temporary
// directories, so that a configuration belonging to whoever is running the
// tests cannot be mistaken for one the test put there.
func isolate(t *testing.T) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	t.Chdir(dir)
	return dir
}

// stubSetupPrompter answers the prompts from a canned list of paths, recording
// the titles it was asked and the suggestions it was offered so the flow's
// questions can be checked.
type stubSetupPrompter struct {
	paths       []string
	confirm     bool
	reconfigure bool
	prune       bool
	titles      []string
	suggestions [][]string
	confirmed   bool
	asked       bool
	pruneAsked  bool
}

func (s *stubSetupPrompter) SelectPath(title, _ string, suggestions []string) (string, error) {
	s.titles = append(s.titles, title)
	s.suggestions = append(s.suggestions, suggestions)
	if len(s.paths) == 0 {
		return "", nil
	}
	path := s.paths[0]
	s.paths = s.paths[1:]
	return path, nil
}

func (s *stubSetupPrompter) ConfirmReconfigure(title, _ string) (bool, error) {
	s.titles = append(s.titles, title)
	s.asked = true
	return s.reconfigure, nil
}

func (s *stubSetupPrompter) ConfirmPrune(title, _ string) (bool, error) {
	s.titles = append(s.titles, title)
	s.pruneAsked = true
	return s.prune, nil
}

func (s *stubSetupPrompter) ConfirmSetup(title, _ string) (bool, error) {
	s.titles = append(s.titles, title)
	s.confirmed = true
	return s.confirm, nil
}

func TestExpandPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "absolute path is untouched", path: "/tmp/plant", want: "/tmp/plant"},
		{name: "relative path is untouched", path: "../plant", want: "../plant"},
		{name: "bare tilde", path: "~", want: home},
		{name: "tilde prefix", path: "~/code/plant", want: filepath.Join(home, "code", "plant")},
		{name: "tilde mid-path is untouched", path: "/tmp/~/plant", want: "/tmp/~/plant"},
		{name: "another user's home is untouched", path: "~other/plant", want: "~other/plant"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := expandPath(tt.path); got != tt.want {
				t.Errorf("expandPath(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestIsInside(t *testing.T) {
	tests := []struct {
		name string
		dir  string
		path string
		want bool
	}{
		{name: "child", dir: "/repo", path: "/repo/orchard.conf", want: true},
		{name: "grandchild", dir: "/repo", path: "/repo/a/b", want: true},
		{name: "the directory itself", dir: "/repo", path: "/repo", want: false},
		{name: "sibling with a shared prefix", dir: "/repo", path: "/repo-worktrees", want: false},
		{name: "unrelated", dir: "/repo", path: "/elsewhere", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isInside(tt.dir, tt.path); got != tt.want {
				t.Errorf("isInside(%q, %q) = %v, want %v", tt.dir, tt.path, got, tt.want)
			}
		})
	}
}

func TestResolveRootTree(t *testing.T) {
	repo := tempRepo(t)

	t.Run("a subdirectory resolves to the repository root", func(t *testing.T) {
		sub := filepath.Join(repo, "pkg", "inner")
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		got, err := resolveRootTree(sub)
		if err != nil {
			t.Fatalf("resolveRootTree() error = %v", err)
		}
		if got != repo {
			t.Errorf("resolveRootTree(%q) = %q, want %q", sub, got, repo)
		}
	})

	t.Run("a directory outside a repository is rejected", func(t *testing.T) {
		// A temporary directory could still sit inside a repository on some
		// machines, so only assert on the error when it really is outside one.
		outside := t.TempDir()
		if err := exec.Command("git", "-C", outside, "rev-parse", "--show-toplevel").Run(); err == nil {
			t.Skip("the temporary directory is inside a git repository")
		}
		if _, err := resolveRootTree(outside); err == nil {
			t.Errorf("resolveRootTree(%q) error = nil, want an error", outside)
		}
	})

	t.Run("a bare repository is rejected by name", func(t *testing.T) {
		bare := filepath.Join(t.TempDir(), "origin.git")
		if out, err := exec.Command("git", "init", "--quiet", "--bare", bare).CombinedOutput(); err != nil {
			t.Fatalf("git init --bare: %v: %s", err, out)
		}
		_, err := resolveRootTree(bare)
		if err == nil || !strings.Contains(err.Error(), "bare repository") {
			t.Errorf("resolveRootTree(%q) error = %v, want it to name the bare repository", bare, err)
		}
	})

	t.Run("a missing directory is rejected", func(t *testing.T) {
		if _, err := resolveRootTree(filepath.Join(repo, "nope")); err == nil {
			t.Errorf("resolveRootTree() error = nil, want an error")
		}
	})

	t.Run("a file is rejected", func(t *testing.T) {
		file := filepath.Join(repo, "README")
		if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		if _, err := resolveRootTree(file); err == nil {
			t.Errorf("resolveRootTree(%q) error = nil, want an error", file)
		}
	})
}

func TestDedupePaths(t *testing.T) {
	got := dedupePaths([]string{"/a", "", "/b", "/a", "/b", "/c"})
	want := []string{"/a", "/b", "/c"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("dedupePaths() = %v, want %v", got, want)
	}
}

func TestDefaultPlantDirs(t *testing.T) {
	tests := []struct {
		name       string
		workDir    string
		configured string
		want       []string
	}{
		{
			name:    "the working directory is offered last",
			workDir: "/elsewhere",
			want:    []string{"/code", "/code/plants", "/elsewhere"},
		},
		{
			name:    "a working directory that repeats a suggestion is offered once",
			workDir: "/code",
			want:    []string{"/code", "/code/plants"},
		},
		{
			name:    "a working directory inside the root tree is not offered",
			workDir: "/code/repo/cmd",
			want:    []string{"/code", "/code/plants"},
		},
		{
			name:    "the root tree itself is not offered",
			workDir: "/code/repo",
			want:    []string{"/code", "/code/plants"},
		},
		{
			name:       "a configured directory leads",
			workDir:    "/code",
			configured: "/plant",
			want:       []string{"/plant", "/code", "/code/plants"},
		},
		{
			name:       "a configured directory that repeats a suggestion still leads once",
			workDir:    "/code",
			configured: "/code",
			want:       []string{"/code", "/code/plants"},
		},
		{
			name:       "a stale configured directory inside the root tree is dropped",
			workDir:    "/code",
			configured: "/code/repo/worktrees",
			want:       []string{"/code", "/code/plants"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := defaultPlantDirs("/code/repo", tt.workDir, tt.configured)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("defaultPlantDirs() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDefaultConfigPaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	global := filepath.Join(home, ".config", "orchard", "orchard.conf")

	tests := []struct {
		name       string
		workDir    string
		configured string
		want       []string
	}{
		{
			name:    "the working directory is offered after the root tree",
			workDir: "/elsewhere",
			want:    []string{"/code/repo/orchard.conf", "/elsewhere/orchard.conf", global},
		},
		{
			name:    "a working directory inside the root tree is offered too",
			workDir: "/code/repo/cmd",
			want:    []string{"/code/repo/orchard.conf", "/code/repo/cmd/orchard.conf", global},
		},
		{
			name:    "a working directory that is the root tree is offered once",
			workDir: "/code/repo",
			want:    []string{"/code/repo/orchard.conf", global},
		},
		{
			name:       "an existing file leads",
			workDir:    "/code/repo",
			configured: global,
			want:       []string{global, "/code/repo/orchard.conf"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := defaultConfigPaths("/code/repo", tt.workDir, tt.configured)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("defaultConfigPaths() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestExistingSetup(t *testing.T) {
	// Somewhere no stray configuration can be found, so that only the files
	// this test writes are picked up.
	home := t.TempDir()
	t.Setenv("HOME", home)
	repo := t.TempDir()
	workDir := t.TempDir()

	write := func(t *testing.T, path, content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		t.Cleanup(func() { _ = os.Remove(path) })
	}
	valid := "root_tree = /code/repo\nplant_dir = /code\n"

	t.Run("nothing configured", func(t *testing.T) {
		path, cfg := existingSetup(repo, workDir, "")
		if path != "" || cfg != nil {
			t.Errorf("existingSetup() = %q, %v, want no configuration", path, cfg)
		}
	})

	t.Run("the root tree comes first", func(t *testing.T) {
		write(t, filepath.Join(repo, configFileName), valid)
		write(t, filepath.Join(workDir, configFileName), "root_tree = /other\nplant_dir = /other\n")
		path, cfg := existingSetup(repo, workDir, "")
		if path != filepath.Join(repo, configFileName) {
			t.Fatalf("existingSetup() path = %q, want the one in the root tree", path)
		}
		if cfg == nil || cfg.PlantDir != "/code" {
			t.Errorf("existingSetup() config = %v, want plant_dir /code", cfg)
		}
	})

	t.Run("the working directory is searched next", func(t *testing.T) {
		write(t, filepath.Join(workDir, configFileName), valid)
		path, _ := existingSetup(repo, workDir, "")
		if path != filepath.Join(workDir, configFileName) {
			t.Errorf("existingSetup() path = %q, want the one in the working directory", path)
		}
	})

	t.Run("the global path is searched last", func(t *testing.T) {
		if err := os.MkdirAll(filepath.Dir(globalConfigPath()), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		write(t, globalConfigPath(), valid)
		path, _ := existingSetup(repo, workDir, "")
		if path != globalConfigPath() {
			t.Errorf("existingSetup() path = %q, want the global one", path)
		}
	})

	t.Run("an explicit path is the only candidate", func(t *testing.T) {
		write(t, filepath.Join(repo, configFileName), valid)
		explicit := filepath.Join(workDir, "custom.conf")
		if path, _ := existingSetup(repo, workDir, explicit); path != "" {
			t.Errorf("existingSetup() path = %q, want nothing for a missing explicit path", path)
		}
		write(t, explicit, valid)
		if path, _ := existingSetup(repo, workDir, explicit); path != explicit {
			t.Errorf("existingSetup() path = %q, want %q", path, explicit)
		}
	})

	t.Run("an unparsable file still counts as found", func(t *testing.T) {
		write(t, filepath.Join(repo, configFileName), "this is not a configuration")
		path, cfg := existingSetup(repo, workDir, "")
		if path != filepath.Join(repo, configFileName) {
			t.Errorf("existingSetup() path = %q, want the unparsable file", path)
		}
		if cfg != nil {
			t.Errorf("existingSetup() config = %v, want nil", cfg)
		}
	})

	t.Run("a stale file is read without complaint", func(t *testing.T) {
		// The directories it names are long gone, which readConfig rejects but
		// setup still needs to report.
		write(t, filepath.Join(repo, configFileName), "root_tree = /gone\nplant_dir = /gone-too\n")
		_, cfg := existingSetup(repo, workDir, "")
		if cfg == nil || cfg.RootTree != "/gone" {
			t.Errorf("existingSetup() config = %v, want the stale values", cfg)
		}
	})
}

func TestExistingSummary(t *testing.T) {
	t.Run("reports the current values", func(t *testing.T) {
		got := existingSummary(&Config{RootTree: "/code/repo", PlantDir: "/code"})
		if !strings.Contains(got, "root_tree = /code/repo") || !strings.Contains(got, "plant_dir = /code") {
			t.Errorf("existingSummary() = %q, want it to list both values", got)
		}
	})

	t.Run("says so when there are none", func(t *testing.T) {
		if got := existingSummary(nil); !strings.Contains(got, "cannot be read") {
			t.Errorf("existingSummary(nil) = %q, want it to say the file is unreadable", got)
		}
	})
}

func TestCheckPlan(t *testing.T) {
	repo := t.TempDir()
	file := filepath.Join(repo, "file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	tests := []struct {
		name     string
		plantDir string
		wantErr  bool
	}{
		{name: "beside the root tree", plantDir: filepath.Dir(repo), wantErr: false},
		{name: "a directory of its own", plantDir: repo + "-worktrees", wantErr: false},
		{name: "the root tree itself", plantDir: repo, wantErr: true},
		{name: "inside the root tree", plantDir: filepath.Join(repo, "worktrees"), wantErr: true},
		{name: "an existing file", plantDir: file, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkPlan(setupPlan{RootTree: repo, PlantDir: tt.plantDir})
			if (err != nil) != tt.wantErr {
				t.Errorf("checkPlan() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestRenderConfigRoundTrips(t *testing.T) {
	// The written file has to be readable by the same parser orchard uses for
	// every other subcommand.
	rootTree := t.TempDir()
	plantDir := t.TempDir()
	path := filepath.Join(t.TempDir(), configFileName)
	plan := setupPlan{RootTree: rootTree, PlantDir: plantDir, ConfigPath: path}

	if err := os.WriteFile(path, []byte(renderConfig(plan)), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg, err := readConfig(path)
	if err != nil {
		t.Fatalf("readConfig() error = %v", err)
	}
	if cfg.RootTree != rootTree || cfg.PlantDir != plantDir {
		t.Errorf("readConfig() = %+v, want root_tree=%s plant_dir=%s", cfg, rootTree, plantDir)
	}
}

func TestSetupSummary(t *testing.T) {
	plan := setupPlan{RootTree: "/code/repo", PlantDir: "/code", ConfigPath: "/code/repo/orchard.conf"}

	t.Run("lists the configured paths", func(t *testing.T) {
		got := setupSummary(plan, true, false, nil)
		want := "root_tree = /code/repo\nplant_dir = /code"
		if got != want {
			t.Errorf("setupSummary() = %q, want %q", got, want)
		}
	})

	t.Run("warns that the plant directory is created", func(t *testing.T) {
		got := setupSummary(plan, false, false, nil)
		if !strings.Contains(got, "/code will be created.") {
			t.Errorf("setupSummary() = %q, want it to mention creating the plant directory", got)
		}
	})

	t.Run("warns about replacing an existing file", func(t *testing.T) {
		got := setupSummary(plan, true, true, nil)
		if !strings.Contains(got, "already exists and will be replaced.") {
			t.Errorf("setupSummary() = %q, want it to mention the overwrite", got)
		}
	})

	t.Run("lists the worktrees being removed", func(t *testing.T) {
		got := setupSummary(plan, true, true, []plantedWorktree{{Name: "wt1"}, {Name: "wt2"}})
		if !strings.Contains(got, "2 worktree(s) and their branches will be removed first:") {
			t.Errorf("setupSummary() = %q, want it to count the removals", got)
		}
		if !strings.Contains(got, "wt1\nwt2") {
			t.Errorf("setupSummary() = %q, want it to name the worktrees", got)
		}
	})
}

func TestStrandedSummary(t *testing.T) {
	got := strandedSummary("/old/plants", []plantedWorktree{{Name: "wt1"}, {Name: "wt2"}})
	if !strings.Contains(got, "/old/plants") {
		t.Errorf("strandedSummary() = %q, want it to name the previous directory", got)
	}
	if !strings.Contains(got, "wt1\nwt2") {
		t.Errorf("strandedSummary() = %q, want it to name the worktrees", got)
	}
}

func TestWorktreeNames(t *testing.T) {
	got := worktreeNames([]plantedWorktree{{Name: "wt1", Path: "/a/wt1"}, {Name: "wt2", Path: "/a/wt2"}})
	want := []string{"wt1", "wt2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("worktreeNames() = %v, want %v", got, want)
	}
}

func TestSetupHints(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	t.Run("the global path is found everywhere", func(t *testing.T) {
		plan := setupPlan{RootTree: "/code/repo", PlantDir: "/code", ConfigPath: globalConfigPath()}
		got := setupHints(plan, false)
		if len(got) != 1 || !strings.Contains(got[0], "any directory") {
			t.Errorf("setupHints() = %v, want a single hint about any directory", got)
		}
	})

	t.Run("a file in the root tree explains where it is found", func(t *testing.T) {
		plan := setupPlan{RootTree: "/code/repo", PlantDir: "/code", ConfigPath: "/code/repo/orchard.conf"}
		got := setupHints(plan, false)
		if len(got) != 3 {
			t.Fatalf("setupHints() = %v, want three hints", got)
		}
		if !strings.Contains(got[0], "/code/repo") {
			t.Errorf("first hint = %q, want it to name the directory", got[0])
		}
		if !strings.Contains(got[2], ".gitignore") {
			t.Errorf("last hint = %q, want it to suggest .gitignore", got[2])
		}
	})

	t.Run("an already ignored file gets no gitignore hint", func(t *testing.T) {
		plan := setupPlan{RootTree: "/code/repo", PlantDir: "/code", ConfigPath: "/code/repo/orchard.conf"}
		for _, hint := range setupHints(plan, true) {
			if strings.Contains(hint, ".gitignore") {
				t.Errorf("setupHints() suggested .gitignore for an ignored file")
			}
		}
	})

	t.Run("a differently named file always needs --config", func(t *testing.T) {
		plan := setupPlan{RootTree: "/code/repo", PlantDir: "/code", ConfigPath: "/elsewhere/custom.conf"}
		got := setupHints(plan, false)
		if len(got) != 1 || !strings.Contains(got[0], "--config /elsewhere/custom.conf") {
			t.Errorf("setupHints() = %v, want a single hint about --config", got)
		}
	})
}

func TestRunSetupNonInteractive(t *testing.T) {
	t.Run("writes the configuration and creates the plant directory", func(t *testing.T) {
		repo := tempRepo(t)
		plantDir := filepath.Join(t.TempDir(), "plant")
		p := &stubSetupPrompter{}

		if err := runSetup(setupOptions{RootTree: repo, PlantDir: plantDir}, p); err != nil {
			t.Fatalf("runSetup() error = %v", err)
		}
		if len(p.titles) != 0 {
			t.Errorf("runSetup() prompted for %v, want no prompts", p.titles)
		}
		if info, err := os.Stat(plantDir); err != nil || !info.IsDir() {
			t.Errorf("plant directory %s was not created: %v", plantDir, err)
		}

		cfg, err := readConfig(filepath.Join(repo, configFileName))
		if err != nil {
			t.Fatalf("readConfig() error = %v", err)
		}
		if cfg.RootTree != repo || cfg.PlantDir != plantDir {
			t.Errorf("wrote %+v, want root_tree=%s plant_dir=%s", cfg, repo, plantDir)
		}
	})

	t.Run("resolves a relative plant directory against the working directory", func(t *testing.T) {
		repo := tempRepo(t)
		wd := t.TempDir()
		t.Chdir(wd)

		if err := runSetup(setupOptions{RootTree: repo, PlantDir: "plant"}, &stubSetupPrompter{}); err != nil {
			t.Fatalf("runSetup() error = %v", err)
		}
		cfg, err := readConfig(filepath.Join(repo, configFileName))
		if err != nil {
			t.Fatalf("readConfig() error = %v", err)
		}
		want := filepath.Join(wd, "plant")
		if !filepath.IsAbs(cfg.PlantDir) || filepath.Base(cfg.PlantDir) != "plant" {
			t.Errorf("plant_dir = %s, want the absolute form of %s", cfg.PlantDir, want)
		}
	})

	t.Run("honours an explicit configuration path", func(t *testing.T) {
		repo := tempRepo(t)
		plantDir := t.TempDir()
		// A path whose parent does not exist yet, as ~/.config/orchard often is.
		out := filepath.Join(t.TempDir(), "nested", "custom.conf")

		if err := runSetup(setupOptions{RootTree: repo, PlantDir: plantDir, ConfigPath: out}, &stubSetupPrompter{}); err != nil {
			t.Fatalf("runSetup() error = %v", err)
		}
		if _, err := readConfig(out); err != nil {
			t.Errorf("readConfig(%s) error = %v", out, err)
		}
		if _, err := os.Stat(filepath.Join(repo, configFileName)); err == nil {
			t.Errorf("runSetup() also wrote %s, want only the explicit path", configFileName)
		}
	})

	t.Run("writes into a directory given as the configuration path", func(t *testing.T) {
		repo := tempRepo(t)
		plantDir := t.TempDir()
		out := t.TempDir()

		if err := runSetup(setupOptions{RootTree: repo, PlantDir: plantDir, ConfigPath: out}, &stubSetupPrompter{}); err != nil {
			t.Fatalf("runSetup() error = %v", err)
		}
		if _, err := readConfig(filepath.Join(out, configFileName)); err != nil {
			t.Errorf("readConfig() error = %v", err)
		}
	})

	t.Run("refuses to overwrite without --force", func(t *testing.T) {
		repo := tempRepo(t)
		plantDir := t.TempDir()
		out := filepath.Join(repo, configFileName)
		if err := os.WriteFile(out, []byte("root_tree = /old\nplant_dir = /old\n"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		err := runSetup(setupOptions{RootTree: repo, PlantDir: plantDir}, &stubSetupPrompter{})
		if err == nil || !strings.Contains(err.Error(), "--force") {
			t.Fatalf("runSetup() error = %v, want an error mentioning --force", err)
		}
		content, readErr := os.ReadFile(out)
		if readErr != nil {
			t.Fatalf("ReadFile: %v", readErr)
		}
		if !strings.Contains(string(content), "/old") {
			t.Errorf("the existing file was modified: %s", content)
		}
	})

	t.Run("overwrites with --force", func(t *testing.T) {
		repo := tempRepo(t)
		plantDir := t.TempDir()
		out := filepath.Join(repo, configFileName)
		if err := os.WriteFile(out, []byte("root_tree = /old\nplant_dir = /old\n"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		if err := runSetup(setupOptions{RootTree: repo, PlantDir: plantDir, Force: true}, &stubSetupPrompter{}); err != nil {
			t.Fatalf("runSetup() error = %v", err)
		}
		cfg, err := readConfig(out)
		if err != nil {
			t.Fatalf("readConfig() error = %v", err)
		}
		if cfg.PlantDir != plantDir {
			t.Errorf("plant_dir = %s, want %s", cfg.PlantDir, plantDir)
		}
	})

	t.Run("rejects a plant directory inside the root tree", func(t *testing.T) {
		repo := tempRepo(t)
		err := runSetup(setupOptions{RootTree: repo, PlantDir: filepath.Join(repo, "worktrees")}, &stubSetupPrompter{})
		if err == nil || !strings.Contains(err.Error(), "outside the repository") {
			t.Fatalf("runSetup() error = %v, want it to reject a plant directory inside the repository", err)
		}
		if _, err := os.Stat(filepath.Join(repo, configFileName)); err == nil {
			t.Errorf("runSetup() wrote a configuration file despite the error")
		}
	})
}

func TestRunSetupInteractive(t *testing.T) {
	t.Run("asks for both paths and writes the answers", func(t *testing.T) {
		isolate(t)
		repo := tempRepo(t)
		plantDir := filepath.Join(t.TempDir(), "plant")
		out := filepath.Join(t.TempDir(), configFileName)
		p := &stubSetupPrompter{paths: []string{plantDir, out}, confirm: true}

		if err := runSetup(setupOptions{RootTree: repo}, p); err != nil {
			t.Fatalf("runSetup() error = %v", err)
		}
		if p.asked {
			t.Errorf("runSetup() offered to reconfigure an unconfigured repository")
		}
		if len(p.titles) != 3 {
			t.Fatalf("runSetup() asked %v, want a plant directory, a path and a confirmation", p.titles)
		}
		cfg, err := readConfig(out)
		if err != nil {
			t.Fatalf("readConfig() error = %v", err)
		}
		if cfg.RootTree != repo || cfg.PlantDir != plantDir {
			t.Errorf("wrote %+v, want root_tree=%s plant_dir=%s", cfg, repo, plantDir)
		}
	})

	t.Run("offers the working directory for both questions", func(t *testing.T) {
		workDir := isolate(t)
		repo := tempRepo(t)
		p := &stubSetupPrompter{paths: []string{workDir, filepath.Join(workDir, configFileName)}, confirm: true}

		if err := runSetup(setupOptions{RootTree: repo}, p); err != nil {
			t.Fatalf("runSetup() error = %v", err)
		}
		if len(p.suggestions) != 2 {
			t.Fatalf("runSetup() made %d selections, want 2", len(p.suggestions))
		}
		if !slices.Contains(p.suggestions[0], workDir) {
			t.Errorf("plant directory suggestions = %v, want the working directory %s", p.suggestions[0], workDir)
		}
		if want := filepath.Join(workDir, configFileName); !slices.Contains(p.suggestions[1], want) {
			t.Errorf("configuration suggestions = %v, want %s", p.suggestions[1], want)
		}
	})

	t.Run("skips the question --config already answers", func(t *testing.T) {
		isolate(t)
		repo := tempRepo(t)
		plantDir := t.TempDir()
		out := filepath.Join(t.TempDir(), configFileName)
		p := &stubSetupPrompter{paths: []string{plantDir}, confirm: true}

		if err := runSetup(setupOptions{RootTree: repo, ConfigPath: out}, p); err != nil {
			t.Fatalf("runSetup() error = %v", err)
		}
		if len(p.titles) != 2 {
			t.Errorf("runSetup() asked %v, want a plant directory and a confirmation", p.titles)
		}
		if _, err := readConfig(out); err != nil {
			t.Errorf("readConfig() error = %v", err)
		}
	})

	t.Run("writes nothing when the confirmation is declined", func(t *testing.T) {
		isolate(t)
		repo := tempRepo(t)
		out := filepath.Join(t.TempDir(), configFileName)
		plantDir := filepath.Join(t.TempDir(), "plant")
		p := &stubSetupPrompter{paths: []string{plantDir, out}, confirm: false}

		if err := runSetup(setupOptions{RootTree: repo}, p); err != nil {
			t.Fatalf("runSetup() error = %v", err)
		}
		if !p.confirmed {
			t.Fatalf("runSetup() did not ask for confirmation")
		}
		if _, err := os.Stat(out); err == nil {
			t.Errorf("runSetup() wrote %s despite the declined confirmation", out)
		}
		if _, err := os.Stat(plantDir); err == nil {
			t.Errorf("runSetup() created %s despite the declined confirmation", plantDir)
		}
	})

	t.Run("backing out of a prompt writes nothing", func(t *testing.T) {
		isolate(t)
		repo := tempRepo(t)
		p := &stubSetupPrompter{confirm: true}

		if err := runSetup(setupOptions{RootTree: repo}, p); err != nil {
			t.Fatalf("runSetup() error = %v", err)
		}
		if p.confirmed {
			t.Errorf("runSetup() asked for confirmation after the prompt was aborted")
		}
		if _, err := os.Stat(filepath.Join(repo, configFileName)); err == nil {
			t.Errorf("runSetup() wrote a configuration file after the prompt was aborted")
		}
	})
}

func TestRunSetupSecondRun(t *testing.T) {
	// A repository that has already been through setup, with a plant directory
	// that is not one of the suggestions.
	configured := func(t *testing.T) (repo, plantDir, configPath string) {
		t.Helper()
		repo = tempRepo(t)
		plantDir = t.TempDir()
		configPath = filepath.Join(repo, configFileName)
		if err := runSetup(setupOptions{RootTree: repo, PlantDir: plantDir}, &stubSetupPrompter{}); err != nil {
			t.Fatalf("first runSetup() error = %v", err)
		}
		return repo, plantDir, configPath
	}

	t.Run("offers to reconfigure, leading with the current answers", func(t *testing.T) {
		isolate(t)
		repo, plantDir, configPath := configured(t)
		newPlantDir := filepath.Join(t.TempDir(), "elsewhere")
		p := &stubSetupPrompter{paths: []string{newPlantDir, configPath}, reconfigure: true, confirm: true}

		if err := runSetup(setupOptions{RootTree: repo}, p); err != nil {
			t.Fatalf("second runSetup() error = %v", err)
		}
		if !p.asked {
			t.Fatalf("runSetup() did not offer to reconfigure")
		}
		if len(p.suggestions) != 2 {
			t.Fatalf("runSetup() made %d selections, want 2", len(p.suggestions))
		}
		if p.suggestions[0][0] != plantDir {
			t.Errorf("plant directory suggestions = %v, want the configured %s first", p.suggestions[0], plantDir)
		}
		if p.suggestions[1][0] != configPath {
			t.Errorf("configuration suggestions = %v, want the existing %s first", p.suggestions[1], configPath)
		}
		cfg, err := readConfig(configPath)
		if err != nil {
			t.Fatalf("readConfig() error = %v", err)
		}
		if cfg.PlantDir != newPlantDir {
			t.Errorf("plant_dir = %s, want the new %s", cfg.PlantDir, newPlantDir)
		}
	})

	t.Run("keeps the configuration when reconfiguring is declined", func(t *testing.T) {
		isolate(t)
		repo, plantDir, configPath := configured(t)
		p := &stubSetupPrompter{reconfigure: false, confirm: true}

		if err := runSetup(setupOptions{RootTree: repo}, p); err != nil {
			t.Fatalf("second runSetup() error = %v", err)
		}
		if len(p.titles) != 1 {
			t.Errorf("runSetup() asked %v, want only whether to reconfigure", p.titles)
		}
		cfg, err := readConfig(configPath)
		if err != nil {
			t.Fatalf("readConfig() error = %v", err)
		}
		if cfg.PlantDir != plantDir {
			t.Errorf("plant_dir = %s, want the original %s", cfg.PlantDir, plantDir)
		}
	})

	t.Run("finds a configuration in the working directory", func(t *testing.T) {
		workDir := isolate(t)
		repo := tempRepo(t)
		if err := os.WriteFile(filepath.Join(workDir, configFileName),
			[]byte("root_tree = /gone\nplant_dir = /gone-too\n"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		p := &stubSetupPrompter{reconfigure: false}

		if err := runSetup(setupOptions{RootTree: repo}, p); err != nil {
			t.Fatalf("runSetup() error = %v", err)
		}
		if !p.asked {
			t.Errorf("runSetup() did not offer to reconfigure a stale configuration in the working directory")
		}
	})

	t.Run("a non-interactive run is not offered the choice", func(t *testing.T) {
		isolate(t)
		repo, _, _ := configured(t)
		p := &stubSetupPrompter{}

		err := runSetup(setupOptions{RootTree: repo, PlantDir: t.TempDir()}, p)
		if err == nil || !strings.Contains(err.Error(), "--force") {
			t.Errorf("runSetup() error = %v, want the --force error rather than a prompt", err)
		}
		if p.asked {
			t.Errorf("runSetup() prompted during a non-interactive run")
		}
	})
}

func TestHuhPrompterSelectPath(t *testing.T) {
	suggestions := []string{"/code", "/code/plants"}

	// In accessible mode a select is answered by entering the option's number;
	// the free-form entry is the last one, and opens an input.
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "first suggestion", input: "1\n", want: "/code"},
		{name: "second suggestion", input: "2\n", want: "/code/plants"},
		{name: "free-form entry", input: "3\n/elsewhere/plant\n", want: "/elsewhere/plant"},
		{name: "free-form entry is trimmed", input: "3\n  /elsewhere/plant  \n", want: "/elsewhere/plant"},
		{name: "out of range reprompts", input: "9\n1\n", want: "/code"},
		{name: "an empty free-form entry reprompts", input: "3\n\n/elsewhere/plant\n", want: "/elsewhere/plant"},
		{name: "EOF on the free-form entry gives no path", input: "3\n", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := accessiblePrompter(tt.input).SelectPath("Where?", "", suggestions)
			if err != nil {
				t.Fatalf("SelectPath() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("SelectPath() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestHuhPrompterConfirmSetup(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{name: "yes", input: "y\n", want: true},
		{name: "no", input: "n\n", want: false},
		{name: "empty defaults to no", input: "\n", want: false},
		{name: "EOF declines", input: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := accessiblePrompter(tt.input).ConfirmSetup("Write?", "")
			if err != nil {
				t.Fatalf("ConfirmSetup() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("ConfirmSetup() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHuhPrompterSelectPathEOF(t *testing.T) {
	// huh's accessible select indexes into its options with whatever the
	// prompt returns, so an answer it cannot make sense of has to land on a
	// real option rather than on nothing.
	t.Run("EOF falls back to the first suggestion", func(t *testing.T) {
		got, err := accessiblePrompter("").SelectPath("Where?", "", []string{"/a", "/b"})
		if err != nil {
			t.Fatalf("SelectPath() error = %v", err)
		}
		if got != "/a" {
			t.Errorf("SelectPath() = %q, want %q", got, "/a")
		}
	})

	t.Run("an invalid answer then EOF falls back to the first suggestion", func(t *testing.T) {
		got, err := accessiblePrompter("nope\n").SelectPath("Where?", "", []string{"/a", "/b"})
		if err != nil {
			t.Fatalf("SelectPath() error = %v", err)
		}
		if got != "/a" {
			t.Errorf("SelectPath() = %q, want %q", got, "/a")
		}
	})

	t.Run("with no suggestions the path is asked for directly", func(t *testing.T) {
		got, err := accessiblePrompter("/typed\n").SelectPath("Where?", "", nil)
		if err != nil {
			t.Fatalf("SelectPath() error = %v", err)
		}
		if got != "/typed" {
			t.Errorf("SelectPath() = %q, want %q", got, "/typed")
		}
	})
}

// plantRepo creates a repository with a commit and plants the named worktrees
// in a directory of its own, the way `orchard add` would.
func plantRepo(t *testing.T, names ...string) (repo, plantDir string) {
	t.Helper()
	repo = tempRepo(t)
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=orchard", "GIT_AUTHOR_EMAIL=orchard@example.com",
			"GIT_COMMITTER_NAME=orchard", "GIT_COMMITTER_EMAIL=orchard@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("commit", "--quiet", "--allow-empty", "-m", "initial")

	plantDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	for _, name := range names {
		run("worktree", "add", "--quiet", "-b", name, filepath.Join(plantDir, name))
	}
	return repo, plantDir
}

// planted reports the names orchard would list for a plant directory.
func planted(t *testing.T, repo, plantDir string) []string {
	t.Helper()
	worktrees, err := cliGitClient{}.listWorktrees(repo)
	if err != nil {
		t.Fatalf("listWorktrees: %v", err)
	}
	return worktreeNames(filterPlantedWorktrees(worktrees, repo, plantDir))
}

func branchExists(t *testing.T, repo, name string) bool {
	t.Helper()
	exists, err := cliGitClient{}.BranchExists(repo, name)
	if err != nil {
		t.Fatalf("BranchExists: %v", err)
	}
	return exists
}

func TestStrandedWorktrees(t *testing.T) {
	repo, plantDir := plantRepo(t, "wt1", "wt2")
	previous := &Config{RootTree: repo, PlantDir: plantDir}

	t.Run("lists what a move would leave behind", func(t *testing.T) {
		got := worktreeNames(strandedWorktrees(repo, previous, "/somewhere/else"))
		want := []string{"wt1", "wt2"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("strandedWorktrees() = %v, want %v", got, want)
		}
	})

	t.Run("nothing is stranded when the directory is unchanged", func(t *testing.T) {
		if got := strandedWorktrees(repo, previous, plantDir); got != nil {
			t.Errorf("strandedWorktrees() = %v, want nothing", got)
		}
	})

	t.Run("nothing is stranded without a previous configuration", func(t *testing.T) {
		if got := strandedWorktrees(repo, nil, "/somewhere/else"); got != nil {
			t.Errorf("strandedWorktrees() = %v, want nothing", got)
		}
	})

	t.Run("another repository's worktrees are left alone", func(t *testing.T) {
		other := &Config{RootTree: "/some/other/repo", PlantDir: plantDir}
		if got := strandedWorktrees(repo, other, "/somewhere/else"); got != nil {
			t.Errorf("strandedWorktrees() = %v, want nothing for another repository", got)
		}
	})
}

func TestRunSetupPrunesStrandedWorktrees(t *testing.T) {
	// A repository already set up, with worktrees planted, about to be pointed
	// at a different plant directory.
	moved := func(t *testing.T) (repo, oldPlantDir, newPlantDir, configPath string) {
		t.Helper()
		isolate(t)
		repo, oldPlantDir = plantRepo(t, "wt1", "wt2")
		configPath = filepath.Join(repo, configFileName)
		if err := runSetup(setupOptions{RootTree: repo, PlantDir: oldPlantDir}, &stubSetupPrompter{}); err != nil {
			t.Fatalf("first runSetup() error = %v", err)
		}
		newPlantDir = filepath.Join(t.TempDir(), "plants")
		return repo, oldPlantDir, newPlantDir, configPath
	}

	t.Run("removes them once confirmed", func(t *testing.T) {
		repo, oldPlantDir, newPlantDir, configPath := moved(t)
		p := &stubSetupPrompter{
			paths:       []string{newPlantDir, configPath},
			reconfigure: true, prune: true, confirm: true,
		}

		if err := runSetup(setupOptions{RootTree: repo}, p); err != nil {
			t.Fatalf("runSetup() error = %v", err)
		}
		if !p.pruneAsked {
			t.Fatalf("runSetup() removed worktrees without asking")
		}
		if got := planted(t, repo, oldPlantDir); len(got) != 0 {
			t.Errorf("worktrees left in the previous directory: %v", got)
		}
		for _, name := range []string{"wt1", "wt2"} {
			if branchExists(t, repo, name) {
				t.Errorf("branch %s still exists, so `orchard add %s` would still fail", name, name)
			}
		}
		cfg, err := readConfig(configPath)
		if err != nil {
			t.Fatalf("readConfig() error = %v", err)
		}
		if cfg.PlantDir != newPlantDir {
			t.Errorf("plant_dir = %s, want %s", cfg.PlantDir, newPlantDir)
		}
	})

	t.Run("leaves them when declined, and still moves", func(t *testing.T) {
		repo, oldPlantDir, newPlantDir, configPath := moved(t)
		p := &stubSetupPrompter{
			paths:       []string{newPlantDir, configPath},
			reconfigure: true, prune: false, confirm: true,
		}

		if err := runSetup(setupOptions{RootTree: repo}, p); err != nil {
			t.Fatalf("runSetup() error = %v", err)
		}
		if !p.pruneAsked {
			t.Fatalf("runSetup() did not ask about the stranded worktrees")
		}
		if got := planted(t, repo, oldPlantDir); len(got) != 2 {
			t.Errorf("worktrees in the previous directory = %v, want both kept", got)
		}
		cfg, err := readConfig(configPath)
		if err != nil {
			t.Fatalf("readConfig() error = %v", err)
		}
		if cfg.PlantDir != newPlantDir {
			t.Errorf("plant_dir = %s, want the move to have happened anyway", cfg.PlantDir)
		}
	})

	t.Run("does not ask when the plant directory is unchanged", func(t *testing.T) {
		repo, oldPlantDir, _, configPath := moved(t)
		p := &stubSetupPrompter{
			paths:       []string{oldPlantDir, configPath},
			reconfigure: true, confirm: true,
		}

		if err := runSetup(setupOptions{RootTree: repo}, p); err != nil {
			t.Fatalf("runSetup() error = %v", err)
		}
		if p.pruneAsked {
			t.Errorf("runSetup() asked about stranded worktrees when nothing moved")
		}
		if got := planted(t, repo, oldPlantDir); len(got) != 2 {
			t.Errorf("worktrees = %v, want both kept", got)
		}
	})

	t.Run("a declined write removes nothing", func(t *testing.T) {
		repo, oldPlantDir, newPlantDir, configPath := moved(t)
		p := &stubSetupPrompter{
			paths:       []string{newPlantDir, configPath},
			reconfigure: true, prune: true, confirm: false,
		}

		if err := runSetup(setupOptions{RootTree: repo}, p); err != nil {
			t.Fatalf("runSetup() error = %v", err)
		}
		if got := planted(t, repo, oldPlantDir); len(got) != 2 {
			t.Errorf("worktrees = %v, want both kept after the write was cancelled", got)
		}
	})

	t.Run("a non-interactive run keeps them unless --prune", func(t *testing.T) {
		repo, oldPlantDir, newPlantDir, _ := moved(t)

		if err := runSetup(setupOptions{RootTree: repo, PlantDir: newPlantDir, Force: true}, &stubSetupPrompter{}); err != nil {
			t.Fatalf("runSetup() error = %v", err)
		}
		if got := planted(t, repo, oldPlantDir); len(got) != 2 {
			t.Errorf("worktrees = %v, want both kept without --prune", got)
		}
	})

	t.Run("a non-interactive run with --prune removes them", func(t *testing.T) {
		repo, oldPlantDir, newPlantDir, _ := moved(t)

		err := runSetup(setupOptions{RootTree: repo, PlantDir: newPlantDir, Force: true, Prune: true}, &stubSetupPrompter{})
		if err != nil {
			t.Fatalf("runSetup() error = %v", err)
		}
		if got := planted(t, repo, oldPlantDir); len(got) != 0 {
			t.Errorf("worktrees left in the previous directory: %v", got)
		}
		if branchExists(t, repo, "wt1") {
			t.Errorf("branch wt1 still exists")
		}
	})
}
