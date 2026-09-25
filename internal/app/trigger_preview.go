package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	meshcore "github.com/meshcore-go/meshcore-go"

	"github.com/meshcore-go/OwlShack/internal/api"
	"github.com/meshcore-go/OwlShack/internal/config"
	"github.com/meshcore-go/OwlShack/internal/node/companion"
)

func triggerTestConfig(in api.TriggerTestInput) config.TriggerConfig {
	cfg := config.TriggerConfig{Type: in.Type, URL: in.URL, Template: in.Template}
	if len(in.Match) > 0 {
		cfg.Match = &in.Match
	}
	return cfg
}

func (b *backend) TestTriggerItems(ctx context.Context, in api.TriggerTestInput) ([]api.TriggerTestItem, error) {
	items, err := b.feedPreview.Items(ctx, triggerTestConfig(in))
	if err != nil {
		return nil, api.Invalid(err)
	}
	out := make([]api.TriggerTestItem, 0, len(items))
	for _, it := range items {
		row := api.TriggerTestItem{ID: it.ID, Title: it.Title, Link: it.Link}
		if !it.Published.IsZero() {
			at := it.Published.UTC().Format(time.RFC3339)
			row.Published = &at
		}
		out = append(out, row)
	}
	return out, nil
}

func (b *backend) TestTriggerRender(ctx context.Context, in api.TriggerTestInput) (api.TriggerTestRender, error) {
	// The companion sends it, so its name is both {{.BotName}} and the prefix a channel message spends bytes on.
	comp, err := b.db.Companions.Get(ctx, in.CompanionID)
	if errors.Is(err, sql.ErrNoRows) {
		return api.TriggerTestRender{}, api.Invalid(fmt.Errorf("no companion %d to send as", in.CompanionID))
	}
	if err != nil {
		return api.TriggerTestRender{}, err
	}
	if in.ItemID == "" {
		return api.TriggerTestRender{}, api.Invalid(errors.New("itemId is required: pick an item to render against"))
	}
	res, err := b.feedPreview.Render(ctx, triggerTestConfig(in), comp.Name, in.ItemID)
	if err != nil {
		return api.TriggerTestRender{}, api.Invalid(err)
	}
	limit := max(0, meshcore.MaxTextLen-len(comp.Name+": "))
	out := api.TriggerTestRender{
		Message: res.Message, RenderError: res.RenderError, Matched: res.Matched, Captures: res.Captures,
		Bytes: len(res.Message), ChannelText: meshcore.TruncateUTF8(res.Message, limit), ChannelLimit: limit,
		DMLimit: companion.MaxDMTextBytes,
	}
	if out.Captures == nil {
		out.Captures = map[string]string{}
	}
	return out, nil
}
