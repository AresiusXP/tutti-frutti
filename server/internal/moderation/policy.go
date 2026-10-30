package moderation

import (
	"context"
	"fmt"
	"time"

	"github.com/AresiusXP/tutti-frutti/server/internal/safety"
	"github.com/AresiusXP/tutti-frutti/server/internal/store"
)

// LoadPolicy fills p with the blocks and the restrictions in force at now. It
// must return before the room hub starts accepting joins (cmd/server calls it
// before Hub.Start): a hub that served a join before the policy was loaded
// would admit a pair the store says must not share a room. A failure is an
// error and the server must not start; a policy that silently starts empty
// would turn every block and restriction off.
func LoadPolicy(ctx context.Context, mod store.Moderation, p *safety.Policy, now time.Time) (blocks, restrictions int, err error) {
	bs, err := mod.LoadBlocks(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("loading blocks: %w", err)
	}
	rs, err := mod.LoadRestrictions(ctx, now)
	if err != nil {
		return 0, 0, fmt.Errorf("loading restrictions: %w", err)
	}
	p.Load(bs, rs)
	return len(bs), len(rs), nil
}
