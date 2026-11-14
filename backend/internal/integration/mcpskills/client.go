package mcpskills

import (
	"context"

	"github.com/wantemira/e2-engineers/backend/internal/domain"
	"github.com/wantemira/e2-engineers/backend/internal/integration/mcptransport"
)

type Client struct{ connection *mcptransport.Client }

func NewClient(endpoint, secret string) *Client {
	return &Client{connection: mcptransport.NewClient(endpoint, secret)}
}

func (client *Client) Execute(ctx context.Context, actor string, call domain.SkillCall) (domain.SkillReply, error) {
	var reply domain.SkillReply
	err := client.connection.Call(ctx, actor, "execute_skill", call, &reply)
	return reply, err
}
