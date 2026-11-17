package generate

// The local friendbot client: the recipes fund their fixed-seed accounts through the friendbot of the local quickstart
// network, whose URL the launcher exposes. The recipes declare the small interface they use, so
// internal/generate/recipes imports neither this package nor net/http.

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/verticalhorizonai/ltk/internal/generate/recipes"
)

// friendbotTimeout bounds one funding request: the friendbot answers once its transaction is in a ledger.
const friendbotTimeout = 60 * time.Second

// The friendbot is the recipes' Funder.
var _ recipes.Funder = (*Friendbot)(nil)

// Friendbot is the local friendbot of one quickstart network.
type Friendbot struct {
	url  string
	http *http.Client
}

// NewFriendbot returns the client of the friendbot at friendbotURL (a Network's FriendbotURL).
func NewFriendbot(friendbotURL string) *Friendbot {
	return &Friendbot{url: friendbotURL, http: &http.Client{Timeout: friendbotTimeout}}
}

// Fund asks the friendbot to create and fund the account address (a G... strkey). Any answer but 200 is an error
// carrying the friendbot's answer. The address is a public key, never a secret: the fixed seeds are the only keys
// the tool holds, and they stay in the process.
func (f *Friendbot) Fund(ctx context.Context, address string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.url+"?addr="+url.QueryEscape(address), nil)
	if err != nil {
		return fmt.Errorf("friendbot: %w", err)
	}
	resp, err := f.http.Do(req)
	if err != nil {
		return fmt.Errorf("friendbot funding %s: %w", address, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("friendbot funding %s: HTTP %d: %.300s", address, resp.StatusCode, body)
	}
	return nil
}
