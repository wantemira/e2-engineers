package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/wantemira/e2-engineers/backend/internal/domain"
)

func TestKnowledgeAnswerUsesBoundedEvidenceAndOnlyReturnedSources(t *testing.T) {
	for _, scenario := range []string{"answer", "unknown reference", "missing citation", "empty search", "write tool"} {
		t.Run(scenario, func(t *testing.T) {
			evidence := domain.Investigation{Question: "про графану что знаешь?", Purpose: "information", Search: domain.KnowledgeSearch{Query: "Grafana", Hits: []domain.KnowledgeHit{{Reference: "access-guide#Grafana", Excerpt: "Grafana — инструмент визуализации метрик.", Source: domain.SourceLink{Label: "Grafana", URL: "/api/v1/context/documents/access-guide#section-2"}}}}}
			if scenario == "empty search" {
				evidence.Search.Hits = nil
				evidence.Search.Query = "обсервер"
			}
			client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Messages []struct {
						Content string `json:"content"`
					} `json:"messages"`
					Tools []Tool `json:"tools"`
				}
				if json.NewDecoder(r.Body).Decode(&body) != nil || len(body.Tools) != 1 || body.Tools[0].Function.Name != "answer_from_knowledge" || len(body.Messages) != 2 {
					t.Error("invalid bounded synthesis request")
					w.WriteHeader(500)
					return
				}
				if !strings.Contains(body.Messages[1].Content, `"purpose":"information"`) {
					t.Error("intent was lost")
				}
				refs := []string{"access-guide#Grafana"}
				text := "Grafana показывает метрики на дашбордах."
				name := "answer_from_knowledge"
				switch scenario {
				case "unknown reference":
					refs = []string{"foreign#secret"}
				case "missing citation":
					refs = nil
				case "empty search":
					refs = nil
					text = "В материалах названия нет. Это внутренний инструмент или паттерн Observer?"
				case "write tool":
					name = "send_mentor_message"
				}
				args, _ := json.Marshal(map[string]any{"text": text, "source_references": refs})
				writeCall(w, name, string(args))
			})
			message, err := client.AnswerFromKnowledge(context.Background(), domain.ChatInput{Text: evidence.Question}, evidence)
			invalid := scenario == "unknown reference" || scenario == "missing citation" || scenario == "write tool"
			if invalid {
				if err == nil {
					t.Fatal("unverified model result accepted")
				}
				return
			}
			if err != nil || message.MentorDraft != "" || message.Help != nil {
				t.Fatalf("invalid answer: %+v %v", message, err)
			}
			if scenario == "answer" && (len(message.Links) != 1 || message.Links[0].URL != evidence.Search.Hits[0].Source.URL) {
				t.Fatal("source lost")
			}
			if scenario == "empty search" && len(message.Links) != 0 {
				t.Fatal("invented source for empty search")
			}
		})
	}
}
