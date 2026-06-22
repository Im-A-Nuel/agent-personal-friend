package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const apiBase = "https://api.github.com"

type Client struct {
	token    string
	gitUser  string
	gitEmail string
	workDir  string
	http     *http.Client
}

func New(token, gitUser, gitEmail, workDir string) *Client {
	return &Client{
		token:    token,
		gitUser:  gitUser,
		gitEmail: gitEmail,
		workDir:  workDir,
		http:     &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *Client) Enabled() bool { return c.token != "" }

// ---------- REST helper ----------

func (c *Client) api(ctx context.Context, method, path string, body interface{}) ([]byte, int, error) {
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, apiBase+path, rdr)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return out, resp.StatusCode, nil
}

func apiError(out []byte, status int) error {
	var e struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(out, &e)
	if e.Message != "" {
		return fmt.Errorf("github %d: %s", status, e.Message)
	}
	return fmt.Errorf("github %d", status)
}

// ---------- Authenticated user ----------

func (c *Client) Login(ctx context.Context) (string, error) {
	out, status, err := c.api(ctx, http.MethodGet, "/user", nil)
	if err != nil {
		return "", err
	}
	if status != 200 {
		return "", apiError(out, status)
	}
	var u struct {
		Login string `json:"login"`
	}
	if err := json.Unmarshal(out, &u); err != nil {
		return "", err
	}
	return u.Login, nil
}

// ---------- Create repo ----------

// CreateRepo membuat repo baru. org=="" → repo pribadi. Mengembalikan html_url + clone_url.
func (c *Client) CreateRepo(ctx context.Context, name, desc string, private bool, org string) (htmlURL string, err error) {
	body := map[string]interface{}{
		"name":        name,
		"description": desc,
		"private":     private,
		"auto_init":   true, // bikin commit awal + README → repo langsung bisa di-clone
	}
	path := "/user/repos"
	if org != "" {
		path = "/orgs/" + org + "/repos"
	}
	out, status, err := c.api(ctx, http.MethodPost, path, body)
	if err != nil {
		return "", err
	}
	if status != 201 {
		return "", apiError(out, status)
	}
	var r struct {
		HTMLURL string `json:"html_url"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return "", err
	}
	return r.HTMLURL, nil
}

// ---------- Agentic work ----------

type WorkResult struct {
	PRURL    string
	Branch   string
	NoChange bool
}

// Work meng-clone repo, menjalankan Claude CLI untuk mengerjakan task, commit, push branch, dan buka PR.
// progress dipanggil tiap tahap untuk update ke user.
func (c *Client) Work(ctx context.Context, owner, repo, task string, progress func(string)) (*WorkResult, error) {
	if owner == "" {
		login, err := c.Login(ctx)
		if err != nil {
			return nil, fmt.Errorf("resolve owner: %w", err)
		}
		owner = login
	}

	// default branch
	base, err := c.defaultBranch(ctx, owner, repo)
	if err != nil {
		return nil, fmt.Errorf("ambil default branch: %w", err)
	}

	if err := os.MkdirAll(c.workDir, 0o755); err != nil {
		return nil, err
	}
	dir := filepath.Join(c.workDir, fmt.Sprintf("%s-%d", repo, time.Now().UnixNano()))
	defer os.RemoveAll(dir)

	cloneURL := fmt.Sprintf("https://x-access-token:%s@github.com/%s/%s.git", c.token, owner, repo)

	progress("📥 Clone repo...")
	if err := run(ctx, "", 120*time.Second, "git", "clone", "--depth", "1", "-b", base, cloneURL, dir); err != nil {
		return nil, fmt.Errorf("clone: %w", err)
	}

	branch := "pf/" + slug(task) + "-" + fmt.Sprintf("%d", time.Now().Unix()%100000)
	if err := run(ctx, dir, 30*time.Second, "git", "checkout", "-b", branch); err != nil {
		return nil, fmt.Errorf("buat branch: %w", err)
	}

	progress("🤖 Claude sedang mengerjakan...")
	claudePrompt := task + "\n\nPENTING: Edit file yang diperlukan untuk menyelesaikan tugas ini. JANGAN menjalankan git commit atau git push — cukup ubah file saja."
	if err := run(ctx, dir, 15*time.Minute, "claude", "-p", claudePrompt,
		"--model", "claude-sonnet-4-6", "--dangerously-skip-permissions"); err != nil {
		return nil, fmt.Errorf("claude: %w", err)
	}

	// cek ada perubahan?
	changed, err := hasChanges(ctx, dir)
	if err != nil {
		return nil, err
	}
	if !changed {
		return &WorkResult{NoChange: true, Branch: branch}, nil
	}

	progress("💾 Commit & push...")
	if err := run(ctx, dir, 30*time.Second, "git", "add", "-A"); err != nil {
		return nil, fmt.Errorf("git add: %w", err)
	}
	commitMsg := commitTitle(task)
	if err := run(ctx, dir, 30*time.Second, "git",
		"-c", "user.name="+c.gitUser,
		"-c", "user.email="+c.gitEmail,
		"commit", "-m", commitMsg); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	if err := run(ctx, dir, 120*time.Second, "git", "push", "origin", branch); err != nil {
		return nil, fmt.Errorf("push: %w", err)
	}

	progress("🔀 Buka Pull Request...")
	prURL, err := c.createPR(ctx, owner, repo, commitMsg, task, branch, base)
	if err != nil {
		return nil, fmt.Errorf("buat PR: %w", err)
	}

	return &WorkResult{PRURL: prURL, Branch: branch}, nil
}

func (c *Client) defaultBranch(ctx context.Context, owner, repo string) (string, error) {
	out, status, err := c.api(ctx, http.MethodGet, "/repos/"+owner+"/"+repo, nil)
	if err != nil {
		return "", err
	}
	if status != 200 {
		return "", apiError(out, status)
	}
	var r struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return "", err
	}
	if r.DefaultBranch == "" {
		return "main", nil
	}
	return r.DefaultBranch, nil
}

func (c *Client) createPR(ctx context.Context, owner, repo, title, body, head, base string) (string, error) {
	out, status, err := c.api(ctx, http.MethodPost, "/repos/"+owner+"/"+repo+"/pulls", map[string]interface{}{
		"title": title,
		"body":  "Dibuat otomatis oleh PF Bot.\n\nTugas:\n" + body,
		"head":  head,
		"base":  base,
	})
	if err != nil {
		return "", err
	}
	if status != 201 {
		return "", apiError(out, status)
	}
	var r struct {
		HTMLURL string `json:"html_url"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return "", err
	}
	return r.HTMLURL, nil
}

// ---------- Resolve merge conflict (by PR number) ----------

type ResolveResult struct {
	PRURL      string
	Branch     string
	NoConflict bool // PR sudah bisa merge, tidak ada conflict
}

type prInfo struct {
	HTMLURL string
	HeadRef string
	BaseRef string
	HeadFull string // owner/repo dari head (deteksi fork)
	BaseFull string
}

func (c *Client) getPR(ctx context.Context, owner, repo string, number int) (*prInfo, error) {
	out, status, err := c.api(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/%s/pulls/%d", owner, repo, number), nil)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, apiError(out, status)
	}
	var r struct {
		HTMLURL string `json:"html_url"`
		Head    struct {
			Ref  string `json:"ref"`
			Repo struct {
				FullName string `json:"full_name"`
			} `json:"repo"`
		} `json:"head"`
		Base struct {
			Ref  string `json:"ref"`
			Repo struct {
				FullName string `json:"full_name"`
			} `json:"repo"`
		} `json:"base"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, err
	}
	return &prInfo{
		HTMLURL:  r.HTMLURL,
		HeadRef:  r.Head.Ref,
		BaseRef:  r.Base.Ref,
		HeadFull: r.Head.Repo.FullName,
		BaseFull: r.Base.Repo.FullName,
	}, nil
}

// ResolveConflict meng-clone repo, merge base ke branch PR, pakai Claude untuk
// menyelesaikan conflict, lalu push. TIDAK melakukan merge PR (user yang merge).
func (c *Client) ResolveConflict(ctx context.Context, owner, repo string, prNumber int, progress func(string)) (*ResolveResult, error) {
	if owner == "" {
		login, err := c.Login(ctx)
		if err != nil {
			return nil, fmt.Errorf("resolve owner: %w", err)
		}
		owner = login
	}

	pr, err := c.getPR(ctx, owner, repo, prNumber)
	if err != nil {
		return nil, fmt.Errorf("ambil PR #%d: %w", prNumber, err)
	}
	if pr.HeadFull != "" && pr.HeadFull != fmt.Sprintf("%s/%s", owner, repo) {
		return nil, fmt.Errorf("PR dari fork (%s) belum didukung", pr.HeadFull)
	}

	if err := os.MkdirAll(c.workDir, 0o755); err != nil {
		return nil, err
	}
	dir := filepath.Join(c.workDir, fmt.Sprintf("%s-pr%d-%d", repo, prNumber, time.Now().UnixNano()))
	defer os.RemoveAll(dir)

	cloneURL := fmt.Sprintf("https://x-access-token:%s@github.com/%s/%s.git", c.token, owner, repo)

	progress("📥 Clone repo (full history)...")
	if err := run(ctx, "", 180*time.Second, "git", "clone", cloneURL, dir); err != nil {
		return nil, fmt.Errorf("clone: %w", err)
	}

	// checkout branch PR + set identitas git
	if err := run(ctx, dir, 30*time.Second, "git", "checkout", pr.HeadRef); err != nil {
		return nil, fmt.Errorf("checkout %s: %w", pr.HeadRef, err)
	}
	_ = run(ctx, dir, 10*time.Second, "git", "config", "user.name", c.gitUser)
	_ = run(ctx, dir, 10*time.Second, "git", "config", "user.email", c.gitEmail)

	// coba merge base
	progress(fmt.Sprintf("🔀 Merge %s ke %s...", pr.BaseRef, pr.HeadRef))
	_, _, _ = runCapture(ctx, dir, 60*time.Second, "git", "merge", "origin/"+pr.BaseRef)

	// ada conflict?
	unmerged, _, _ := runCapture(ctx, dir, 30*time.Second, "git", "ls-files", "-u")
	if strings.TrimSpace(unmerged) == "" {
		// tidak ada conflict → batalkan merge, PR sudah bisa merge
		_ = run(ctx, dir, 30*time.Second, "git", "merge", "--abort")
		return &ResolveResult{NoConflict: true, PRURL: pr.HTMLURL, Branch: pr.HeadRef}, nil
	}

	progress("🤖 Claude menyelesaikan conflict...")
	claudePrompt := "Repo ini sedang dalam proses git merge dengan CONFLICT. " +
		"Selesaikan SEMUA merge conflict: buka tiap file yang konflik, hapus marker <<<<<<<, =======, >>>>>>>, " +
		"dan gabungkan maksud kedua sisi dengan benar sehingga kode tetap valid. " +
		"JANGAN menjalankan git commit, git merge, atau git push — cukup edit file sampai tidak ada marker conflict tersisa."
	if err := run(ctx, dir, 15*time.Minute, "claude", "-p", claudePrompt,
		"--model", "claude-sonnet-4-6", "--dangerously-skip-permissions"); err != nil {
		return nil, fmt.Errorf("claude: %w", err)
	}

	// pastikan tidak ada marker tersisa
	markers, code, _ := runCapture(ctx, dir, 30*time.Second, "git", "grep", "-l", "<<<<<<<")
	if code == 0 && strings.TrimSpace(markers) != "" {
		return nil, fmt.Errorf("masih ada conflict marker yang belum selesai:\n%s", strings.TrimSpace(markers))
	}

	progress("💾 Commit & push hasil resolve...")
	if err := run(ctx, dir, 30*time.Second, "git", "add", "-A"); err != nil {
		return nil, fmt.Errorf("git add: %w", err)
	}
	if err := run(ctx, dir, 30*time.Second, "git",
		"-c", "user.name="+c.gitUser, "-c", "user.email="+c.gitEmail,
		"commit", "--no-edit"); err != nil {
		return nil, fmt.Errorf("commit merge: %w", err)
	}
	if err := run(ctx, dir, 120*time.Second, "git", "push", "origin", pr.HeadRef); err != nil {
		return nil, fmt.Errorf("push: %w", err)
	}

	return &ResolveResult{PRURL: pr.HTMLURL, Branch: pr.HeadRef}, nil
}

// ---------- exec helpers ----------

func run(ctx context.Context, dir string, timeout time.Duration, name string, args ...string) error {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if len(msg) > 500 {
			msg = msg[len(msg)-500:]
		}
		return fmt.Errorf("%s: %w\n%s", name, err, msg)
	}
	return nil
}

// runCapture menjalankan command dan mengembalikan output gabungan + exit code (tanpa menganggap non-zero fatal).
func runCapture(ctx context.Context, dir string, timeout time.Duration, name string, args ...string) (string, int, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			return string(out), -1, err
		}
	}
	return string(out), code, nil
}

func hasChanges(ctx context.Context, dir string) (bool, error) {
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, "git", "status", "--porcelain")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(out)) != "", nil
}

func slug(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_':
			b.WriteByte('-')
		}
		if b.Len() >= 30 {
			break
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "task"
	}
	return out
}

func commitTitle(task string) string {
	t := strings.TrimSpace(strings.SplitN(task, "\n", 2)[0])
	if len(t) > 70 {
		t = t[:70]
	}
	return t
}
