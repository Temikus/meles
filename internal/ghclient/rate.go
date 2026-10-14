package ghclient

import (
	"fmt"

	"github.com/google/go-github/v92/github"
)

// RateFloor is the remaining core quota below which meles stops, leaving
// headroom for the user's other tools.
const RateFloor = 200

// CheckRate fails once the remaining quota drops below RateFloor.
func CheckRate(resp *github.Response) error {
	if resp == nil || resp.Rate.Limit == 0 {
		return nil
	}
	if resp.Rate.Remaining < RateFloor {
		return fmt.Errorf("rate limit low: %d of %d left, resets %s", resp.Rate.Remaining, resp.Rate.Limit, resp.Rate.Reset.Format("15:04 MST"))
	}
	return nil
}
