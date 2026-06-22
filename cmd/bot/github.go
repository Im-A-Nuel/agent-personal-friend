package main

import (
	"context"
	"fmt"
	"log"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/ezranhmry/pf/internal/config"
	gh "github.com/ezranhmry/pf/internal/github"
	"github.com/ezranhmry/pf/internal/nlu"
)

func handleRepoCreate(
	msg *tgbotapi.Message,
	bot *tgbotapi.BotAPI,
	cfg *config.Config,
	ctx context.Context,
	parsed *nlu.ParsedMessage,
) {
	client := gh.New(cfg.GitHubToken, cfg.GitHubUser, cfg.GitHubEmail, cfg.RepoWorkDir)
	if !client.Enabled() {
		reply(bot, msg.Chat.ID, "GitHub belum aktif. Set GITHUB_TOKEN di .env ya.")
		return
	}
	if strings.TrimSpace(parsed.RepoName) == "" {
		reply(bot, msg.Chat.ID, "Nama repo-nya apa? Sebutkan ya 😊")
		return
	}

	visibility := "public"
	if parsed.RepoPrivate {
		visibility = "private"
	}
	target := "akun pribadi"
	if parsed.RepoOrg != "" {
		target = "organisasi " + parsed.RepoOrg
	}
	reply(bot, msg.Chat.ID, fmt.Sprintf("⚙️ Membuat repo *%s* (%s) di %s...", parsed.RepoName, visibility, target))

	url, err := client.CreateRepo(ctx, parsed.RepoName, parsed.RepoDesc, parsed.RepoPrivate, parsed.RepoOrg)
	if err != nil {
		log.Printf("repo create: %v", err)
		reply(bot, msg.Chat.ID, fmt.Sprintf("❌ Gagal buat repo: %v", err))
		return
	}
	reply(bot, msg.Chat.ID, fmt.Sprintf("✅ Repo berhasil dibuat!\n%s", url))
}

func handleRepoWork(
	msg *tgbotapi.Message,
	bot *tgbotapi.BotAPI,
	cfg *config.Config,
	ctx context.Context,
	parsed *nlu.ParsedMessage,
) {
	client := gh.New(cfg.GitHubToken, cfg.GitHubUser, cfg.GitHubEmail, cfg.RepoWorkDir)
	if !client.Enabled() {
		reply(bot, msg.Chat.ID, "GitHub belum aktif. Set GITHUB_TOKEN di .env ya.")
		return
	}
	if strings.TrimSpace(parsed.RepoTarget) == "" || strings.TrimSpace(parsed.RepoTask) == "" {
		reply(bot, msg.Chat.ID, "Sebutkan repo dan tugasnya ya. Contoh: \"di repo myapp, tambahkan endpoint /health\"")
		return
	}

	owner, repo := splitRepo(parsed.RepoTarget)

	reply(bot, msg.Chat.ID, fmt.Sprintf("🚀 Mulai kerja di repo *%s*\nTugas: _%s_\n\nIni bisa makan beberapa menit ⏳", parsed.RepoTarget, parsed.RepoTask))

	progress := func(s string) { reply(bot, msg.Chat.ID, s) }

	res, err := client.Work(ctx, owner, repo, parsed.RepoTask, progress)
	if err != nil {
		log.Printf("repo work: %v", err)
		reply(bot, msg.Chat.ID, fmt.Sprintf("❌ Gagal: %v", err))
		return
	}
	if res.NoChange {
		reply(bot, msg.Chat.ID, "ℹ️ Claude tidak mengubah file apa pun. Mungkin tugas sudah selesai atau perlu instruksi lebih jelas.")
		return
	}
	reply(bot, msg.Chat.ID, fmt.Sprintf("✅ Selesai! Pull Request dibuat:\n%s\n\nBranch: `%s`", res.PRURL, res.Branch))
}

func handleRepoResolve(
	msg *tgbotapi.Message,
	bot *tgbotapi.BotAPI,
	cfg *config.Config,
	ctx context.Context,
	parsed *nlu.ParsedMessage,
) {
	client := gh.New(cfg.GitHubToken, cfg.GitHubUser, cfg.GitHubEmail, cfg.RepoWorkDir)
	if !client.Enabled() {
		reply(bot, msg.Chat.ID, "GitHub belum aktif. Set GITHUB_TOKEN di .env ya.")
		return
	}
	if strings.TrimSpace(parsed.RepoTarget) == "" || parsed.RepoPR <= 0 {
		reply(bot, msg.Chat.ID, "Sebutkan repo dan nomor PR-nya ya. Contoh: \"fix conflict PR 3 di repo myapp\"")
		return
	}

	owner, repo := splitRepo(parsed.RepoTarget)

	reply(bot, msg.Chat.ID, fmt.Sprintf("🔧 Resolve conflict PR #%d di repo *%s*\n\nBisa makan beberapa menit ⏳", parsed.RepoPR, parsed.RepoTarget))

	progress := func(s string) { reply(bot, msg.Chat.ID, s) }

	res, err := client.ResolveConflict(ctx, owner, repo, parsed.RepoPR, progress)
	if err != nil {
		log.Printf("repo resolve: %v", err)
		reply(bot, msg.Chat.ID, fmt.Sprintf("❌ Gagal: %v", err))
		return
	}
	if res.NoConflict {
		reply(bot, msg.Chat.ID, fmt.Sprintf("✅ PR #%d tidak ada conflict — langsung bisa di-merge.\n%s", parsed.RepoPR, res.PRURL))
		return
	}
	reply(bot, msg.Chat.ID, fmt.Sprintf("✅ Conflict selesai & di-push!\nBranch `%s` sekarang mergeable.\nReview & merge: %s", res.Branch, res.PRURL))
}

// splitRepo memecah "owner/repo" → (owner, repo). Jika tanpa "/", owner="" (akan di-resolve ke akun token).
func splitRepo(target string) (owner, repo string) {
	target = strings.TrimSpace(target)
	if i := strings.Index(target, "/"); i != -1 {
		return target[:i], target[i+1:]
	}
	return "", target
}
