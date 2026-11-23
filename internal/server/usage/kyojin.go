package usage

import (
	"time"

	"github.com/tidwall/gjson"
)

// kyojinExtractor reads kyojin usage. Kyojin (owned_by "yamz" in /v1/models)
// reports a llama-server style timings block, but counts speculative
// decoding as accepted_draft_tokens and rejected_draft_tokens instead of
// draft_n and draft_n_accepted.
type kyojinExtractor struct{}

func (kyojinExtractor) newParser(path string, start time.Time) usageParser {
	return &kyojinParser{start: start, read: apiReaderFor(path), api: newAPIUsage()}
}

type kyojinParser struct {
	start   time.Time
	read    apiReader
	api     apiUsage
	timings timingBlock
}

func (p *kyojinParser) event(ev gjson.Result) {
	p.timings.readEvent(ev, p.read, &p.api)
}

func (p *kyojinParser) usage() Usage {
	u := p.api.toUsage(p.start)
	applyTimings(&u, p.timings)
	applyKyojinDrafts(&u, p.timings.last)
	return u
}

// applyKyojinDrafts reads accepted_draft_tokens and rejected_draft_tokens
// from a kyojin timings block. Drafted tokens are accepted + rejected. Both
// counters are required so the acceptance rate is never derived from half
// the data.
func applyKyojinDrafts(u *Usage, timings gjson.Result) {
	accepted, rejected := timings.Get("accepted_draft_tokens"), timings.Get("rejected_draft_tokens")
	if accepted.Type != gjson.Number || rejected.Type != gjson.Number {
		return
	}
	u.Tokens.DraftAccTokens = int(accepted.Int())
	u.Tokens.DraftTokens = int(accepted.Int() + rejected.Int())
}
