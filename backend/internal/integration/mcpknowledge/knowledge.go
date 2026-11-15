// Package mcpknowledge is a replaceable, read-only team knowledge boundary.
// The demo searches local Markdown; it never claims to query Avito infrastructure.
package mcpknowledge

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"unicode"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wantemira/e2-engineers/backend/internal/domain"
	"github.com/wantemira/e2-engineers/backend/internal/integration/mcptransport"
)

type TeamLoader interface{ Load() (domain.Team, error) }
type Query struct {
	Text string `json:"text"`
}
type Client struct{ transport *mcptransport.Client }

func NewClient(endpoint, secret string) *Client {
	return &Client{mcptransport.NewClient(endpoint, secret)}
}
func (c *Client) Search(ctx context.Context, actor, query string) (domain.KnowledgeSearch, error) {
	var result domain.KnowledgeSearch
	err := c.transport.Call(ctx, actor, "search_team_knowledge", Query{query}, &result)
	return result, err
}
func NewHandler(secret string, teams TeamLoader) http.Handler {
	handler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		server := mcp.NewServer(&mcp.Implementation{Name: "ai-buddy-knowledge-demo", Version: "1.0.0"}, nil)
		mcp.AddTool(server, &mcp.Tool{Name: "search_team_knowledge", Description: "Read-only lexical search in the authorized demo team's Markdown documents"}, func(ctx context.Context, _ *mcp.CallToolRequest, q Query) (*mcp.CallToolResult, domain.KnowledgeSearch, error) {
			var result domain.KnowledgeSearch
			// Fixed demo identity; a real adapter must use its own actor/team ACL.
			if r.Header.Get("X-Buddy-Actor") != "intern-1" {
				return nil, result, errors.New(`{"code":"NOT_FOUND","message":"Источник недоступен."}`)
			}
			if err := ctx.Err(); err != nil {
				return nil, result, err
			}
			if strings.TrimSpace(q.Text) == "" || len([]rune(q.Text)) > 2000 {
				return nil, result, errors.New(`{"code":"INVALID_INPUT","message":"Некорректный поисковый запрос."}`)
			}
			team, err := teams.Load()
			if err != nil {
				data, _ := json.Marshal(domain.PublicError(err))
				return nil, result, errors.New(string(data))
			}
			return nil, Search(team, q.Text), nil
		})
		return server
	}, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	return mcptransport.Authenticate(secret, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
		handler.ServeHTTP(w, r)
	}))
}
func tokens(text string) []string {
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	seen := map[string]bool{}
	stop := " не да на но из мы вы он по за до со от во об же ли то ещё еще как что где когда почему меня мне мой моя это уже могу получается помогло пробовал проверил нужно нужен хочу ошибка ошибки сервис сервиса сервисе вопрос доступ получить после чтобы который какой какая какая-то есть нет для или при помоги пожалуйста help the and "
	result := []string{}
	for _, w := range words {
		if len([]rune(w)) < 2 || strings.Contains(stop, " "+w+" ") || seen[w] {
			continue
		}
		seen[w] = true
		result = append(result, w)
	}
	return result
}

// Search returns up to five matching sections. This is deliberately lexical, not RAG.
func Search(team domain.Team, query string) domain.KnowledgeSearch {
	result := domain.KnowledgeSearch{Query: query, Hits: []domain.KnowledgeHit{}}
	type ranked struct {
		hit   domain.KnowledgeHit
		score int
	}
	matches := []ranked{}
	words := tokens(query)
	for _, doc := range team.Documents {
		for _, section := range domain.Sections(doc) {
			if strings.TrimSpace(section.Body) == "" {
				continue
			}
			reference := doc.ID
			if section.Title != "" {
				reference += "#" + section.Title
			}
			value := 0
			body := strings.ToLower(section.Title + " " + section.Body)
			for _, word := range words {
				if strings.Contains(body, word) {
					value++
					if strings.Contains(strings.ToLower(section.Title), word) {
						value += 2
					}
				}
			}
			if value == 0 {
				continue
			}
			link, _, ok := domain.DocumentSource(team, reference)
			if !ok {
				continue
			}
			excerpt := []rune(section.Body)
			if len(excerpt) > 1200 {
				excerpt = append(excerpt[:1200], []rune("…")...)
			}
			matches = append(matches, ranked{domain.KnowledgeHit{Reference: reference, Title: link.Label, Excerpt: string(excerpt), Source: link}, value})
		}
	}
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].score > matches[j].score })
	for i, match := range matches {
		if i == 5 {
			break
		}
		result.Hits = append(result.Hits, match.hit)
	}
	return result
}
