package mcpgit

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"time"

	"github.com/wantemira/e2-engineers/backend/internal/domain"
	"github.com/wantemira/e2-engineers/backend/internal/storage/jsonfile"
)

type runCheckArguments struct {
	Number    int    `json:"number"`
	HeadSHA   string `json:"head_sha"`
	ActionKey string `json:"action_key"`
}

type checkAction struct {
	Fingerprint string        `json:"fingerprint"`
	Baseline    string        `json:"baseline"`
	Result      domain.Checks `json:"result"`
}

func checksBaseline(checks domain.Checks) string {
	data, _ := json.Marshal(checks)
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func (server *gitServer) getChecks(ctx context.Context, actor string, input checkArguments) (domain.Checks, error) {
	server.mu.Lock()
	defer server.mu.Unlock()
	revision, err := server.read(ctx, actor, input.Number)
	if err != nil {
		return domain.Checks{}, err
	}
	if revision.PR.HeadSHA != input.HeadSHA {
		return domain.Checks{}, domain.Problem("STALE_HEAD", "Версия PR изменилась.")
	}
	key := fmt.Sprintf("%s:%d:%s", actor, input.Number, input.HeadSHA)
	if latest, ok := server.state.LatestChecks[key]; ok && latest.Baseline == checksBaseline(revision.Checks) {
		return latest.Result, nil
	}
	return revision.Checks, nil
}

func (server *gitServer) runChecks(ctx context.Context, actor string, input runCheckArguments) (domain.Checks, error) {
	if err := ctx.Err(); err != nil {
		return domain.Checks{}, err
	}
	if actor != "intern-1" || input.Number != 17 {
		return domain.Checks{}, domain.Problem("NOT_FOUND", "PR недоступен.")
	}
	if input.ActionKey == "" || len(input.ActionKey) > 128 || input.HeadSHA == "" {
		return domain.Checks{}, domain.Problem("INVALID_INPUT", "Нужны версия PR и ключ запуска CI.")
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	key := actor + ":" + input.ActionKey
	fingerprint := fmt.Sprintf("%d:%s", input.Number, input.HeadSHA)
	if previous, ok := server.state.CheckActions[key]; ok {
		if previous.Fingerprint != fingerprint {
			return domain.Checks{}, domain.Problem("IDEMPOTENCY_CONFLICT", "Ключ использован для другого запуска CI.")
		}
		return previous.Result, nil
	}
	revision, err := server.read(ctx, actor, input.Number)
	if err != nil {
		return domain.Checks{}, err
	}
	if revision.PR.HeadSHA != input.HeadSHA {
		return domain.Checks{}, domain.Problem("STALE_HEAD", "Версия PR изменилась.")
	}
	result := revision.Checks
	result.RunID = "CI-" + rand.Text()
	result.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	action := checkAction{Fingerprint: fingerprint, Baseline: checksBaseline(revision.Checks), Result: result}
	updated := server.state
	updated.CheckActions = maps.Clone(server.state.CheckActions)
	updated.LatestChecks = maps.Clone(server.state.LatestChecks)
	updated.CheckActions[key] = action
	updated.LatestChecks[fmt.Sprintf("%s:%d:%s", actor, input.Number, input.HeadSHA)] = action
	if err := jsonfile.Write(server.statePath, updated); err != nil {
		return domain.Checks{}, err
	}
	server.state = updated
	return result, nil
}
