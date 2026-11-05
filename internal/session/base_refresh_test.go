package session

import (
	"path/filepath"
	"testing"

	"github.com/tomasz-tomczyk/crit/internal/vcs"
)

// initRepoWithOrigin returns a working repo on main with an origin remote and
// a second clone ("teammate") that can push to it.
func initRepoWithOrigin(t *testing.T) (dir, teammate string) {
	t.Helper()
	dir = initTestRepo(t)
	root := t.TempDir()
	bare := filepath.Join(root, "origin.git")
	gitT(t, root, "clone", "-q", "--bare", dir, bare)
	gitT(t, dir, "remote", "add", "origin", bare)
	gitT(t, dir, "fetch", "-q", "origin")
	gitT(t, dir, "remote", "set-head", "origin", "main")
	gitT(t, dir, "branch", "-q", "--set-upstream-to=origin/main", "main")
	teammate = filepath.Join(root, "teammate")
	gitT(t, root, "clone", "-q", bare, teammate)
	gitT(t, teammate, "config", "user.email", "mate@test.com")
	gitT(t, teammate, "config", "user.name", "Mate")
	return dir, teammate
}

func teammatePush(t *testing.T, teammate, name string) {
	t.Helper()
	writeFile(t, filepath.Join(teammate, name), "package mate\n")
	gitT(t, teammate, "add", name)
	gitT(t, teammate, "commit", "-q", "-m", "teammate "+name)
	gitT(t, teammate, "push", "-q", "origin", "main")
}

// chdirToRepo enters dir and clears the cached default-branch detection, which
// is process-global and would otherwise leak between tests.
func chdirToRepo(t *testing.T, dir string) {
	t.Helper()
	chdirForTest(t, dir)
	vcs.ResetDefaultBranchOnceForTest()
	t.Cleanup(vcs.ResetDefaultBranchOnceForTest)
}

func sessionPaths(s *Session) map[string]bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	paths := make(map[string]bool, len(s.Files))
	for _, f := range s.Files {
		paths[f.Path] = true
	}
	return paths
}

func TestRefreshBase_MainPullRebaseDropsTeammateCommits(t *testing.T) {
	dir, teammate := initRepoWithOrigin(t)
	chdirToRepo(t, dir)

	writeFile(t, filepath.Join(dir, "agent.go"), "package main\n")
	sess, err := NewGitSession(&vcs.GitVCS{}, nil)
	if err != nil {
		t.Fatalf("NewGitSession: %v", err)
	}

	gitT(t, dir, "add", "agent.go")
	gitT(t, dir, "commit", "-q", "-m", "agent commit")
	sess.RefreshFileList()
	if !sessionPaths(sess)["agent.go"] {
		t.Fatal("agent commit made after session start must stay visible")
	}

	teammatePush(t, teammate, "teammate.go")
	gitT(t, dir, "pull", "-q", "--rebase", "origin", "main")
	writeFile(t, filepath.Join(dir, "fix.go"), "package main\n")
	sess.RefreshFileList()

	paths := sessionPaths(sess)
	if paths["teammate.go"] {
		t.Fatal("teammate commit pulled in by rebase must not appear in the review")
	}
	if !paths["agent.go"] || !paths["fix.go"] {
		t.Fatalf("expected unpushed agent commit and working-tree fix, got %v", paths)
	}
}

func TestRefreshBase_MainRewrittenStartSHAFallsBackToUpstream(t *testing.T) {
	dir, teammate := initRepoWithOrigin(t)
	chdirToRepo(t, dir)

	writeFile(t, filepath.Join(dir, "local.go"), "package main\n")
	gitT(t, dir, "add", "local.go")
	gitT(t, dir, "commit", "-q", "-m", "unpushed local commit")
	writeFile(t, filepath.Join(dir, "agent.go"), "package main\n")
	sess, err := NewGitSession(&vcs.GitVCS{}, nil)
	if err != nil {
		t.Fatalf("NewGitSession: %v", err)
	}
	start := sess.BaseRef

	teammatePush(t, teammate, "teammate.go")
	gitT(t, dir, "stash", "-q", "-u")
	gitT(t, dir, "pull", "-q", "--rebase", "origin", "main")
	gitT(t, dir, "stash", "pop", "-q")
	sess.RefreshFileList()

	if sess.BaseRef == start {
		t.Fatal("rewritten session-start SHA must be replaced")
	}
	paths := sessionPaths(sess)
	if paths["teammate.go"] {
		t.Fatal("teammate commit must not appear in the review")
	}
	if !paths["agent.go"] {
		t.Fatalf("expected working-tree change to stay visible, got %v", paths)
	}
}

func TestRefreshBase_FeatureBranchRebaseRecomputesMergeBase(t *testing.T) {
	dir, teammate := initRepoWithOrigin(t)
	chdirToRepo(t, dir)

	gitT(t, dir, "checkout", "-q", "-b", "feature")
	writeFile(t, filepath.Join(dir, "feature.go"), "package main\n")
	gitT(t, dir, "add", "feature.go")
	gitT(t, dir, "commit", "-q", "-m", "feature commit")
	sess, err := NewGitSession(&vcs.GitVCS{}, nil)
	if err != nil {
		t.Fatalf("NewGitSession: %v", err)
	}

	teammatePush(t, teammate, "teammate.go")
	gitT(t, dir, "fetch", "-q", "origin")
	gitT(t, dir, "rebase", "-q", "origin/main")
	sess.RefreshFileList()

	paths := sessionPaths(sess)
	if paths["teammate.go"] {
		t.Fatal("upstream commit under the rebased branch must not appear in the review")
	}
	if !paths["feature.go"] {
		t.Fatalf("expected feature commit to stay visible, got %v", paths)
	}
}
