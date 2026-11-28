package bridges

import "fmt"

// Collection chooses products, independently of the scientific Options. Nil
// requests resolve to audit; explicit empty lists are rejected rather than
// silently representing an unmeasured product as a measured zero.
type Collection struct {
	Profile  string   `json:"profile" msgpack:"profile"`
	Channels []string `json:"channels" msgpack:"channels"`
	Scopes   []string `json:"scopes" msgpack:"scopes"`
	Products []string `json:"products" msgpack:"products"`
}

type collectionPlan struct {
	channels, scopes, products map[string]bool
}

func (p *collectionPlan) histories() bool {
	for _, name := range []string{"summaries", "series", "events", "episodes", "conversions"} {
		if p.products[name] {
			return true
		}
	}
	return false
}

// ResolveCollection returns an owned, explicit selection suitable for receipts.
func ResolveCollection(request *Collection) (Collection, error) {
	profile := "audit"
	if request != nil && request.Profile != "" {
		profile = request.Profile
	}
	c := Collection{Profile: profile, Channels: []string{"U", "O", "P", "S"}, Scopes: []string{"pair", "union"}, Products: []string{"summaries", "events", "episodes", "conversions", "group_events", "snapshots"}}
	switch profile {
	case "audit":
	case "plot":
		c.Scopes = []string{"union"}
		c.Products = []string{"summaries", "series"}
	case "lifecycle":
		c.Scopes = []string{"union"}
		c.Products = []string{"summaries", "series", "events", "episodes", "conversions"}
	default:
		return Collection{}, fmt.Errorf("bridges: unknown collection profile %q", profile)
	}
	if request != nil {
		if request.Channels != nil {
			c.Channels = append([]string{}, request.Channels...)
		}
		if request.Scopes != nil {
			c.Scopes = append([]string{}, request.Scopes...)
		}
		if request.Products != nil {
			c.Products = append([]string{}, request.Products...)
		}
	}
	for _, item := range []struct {
		name            string
		values, allowed []string
	}{
		{"channels", c.Channels, []string{"U", "O", "P", "S"}},
		{"scopes", c.Scopes, []string{"pair", "union"}},
		{"products", c.Products, []string{"summaries", "series", "events", "episodes", "conversions", "group_events", "snapshots", "details"}},
	} {
		if len(item.values) == 0 {
			return Collection{}, fmt.Errorf("bridges: collection %s cannot be empty", item.name)
		}
		seen := map[string]bool{}
		for _, value := range item.values {
			valid := false
			for _, allowed := range item.allowed {
				if value == allowed {
					valid = true
					break
				}
			}
			if !valid || seen[value] {
				return Collection{}, fmt.Errorf("bridges: invalid or duplicate collection %s value %q", item.name, value)
			}
			seen[value] = true
		}
	}
	// Holder details are a snapshot product; make the dependency explicit in
	// the resolved selection instead of silently collecting and discarding it.
	if c.IncludesProduct("details") && !c.IncludesProduct("snapshots") {
		c.Products = append(c.Products, "snapshots")
	}
	return c, nil
}

func compileCollection(c *Collection) (*collectionPlan, error) {
	resolved, err := ResolveCollection(c)
	if err != nil {
		return nil, err
	}
	p := &collectionPlan{channels: map[string]bool{}, scopes: map[string]bool{}, products: map[string]bool{}}
	for _, v := range resolved.Channels {
		p.channels[v] = true
	}
	for _, v := range resolved.Scopes {
		p.scopes[v] = true
	}
	for _, v := range resolved.Products {
		p.products[v] = true
	}
	return p, nil
}

// IncludesProduct reports a resolved selection's product capabilities.
func (c Collection) IncludesProduct(name string) bool {
	for _, v := range c.Products {
		if v == name {
			return true
		}
	}
	return false
}

func channelCode(channel string) string {
	switch channel {
	case ChannelOpinion:
		return "O"
	case ChannelPost:
		return "P"
	case ChannelStructural:
		return "S"
	default:
		return "U"
	}
}

// StepStats associates events at Step with their original epoch and integrates
// preceding holder states on [IntervalStart,IntervalEnd). An epoch close can
// share Step with a new epoch's opening record; neither record is overwritten.
type StepStats struct {
	Scope                    string   `json:"scope" msgpack:"scope"`
	GroupIDs                 [2]int64 `json:"group_ids" msgpack:"group_ids"`
	Epoch                    int      `json:"epoch" msgpack:"epoch"`
	Channel                  string   `json:"channel" msgpack:"channel"`
	Side                     *int     `json:"side,omitempty" msgpack:"side,omitempty"`
	Step                     int      `json:"step" msgpack:"step"`
	IntervalStart            int      `json:"interval_start" msgpack:"interval_start"`
	IntervalEnd              int      `json:"interval_end" msgpack:"interval_end"`
	Closing                  bool     `json:"closing" msgpack:"closing"`
	Population               int      `json:"population" msgpack:"population"`
	Active                   int      `json:"active" msgpack:"active"`
	Known                    int      `json:"known" msgpack:"known"`
	NeverSeen                int      `json:"never_seen" msgpack:"never_seen"`
	SeenInactive             int      `json:"seen_inactive" msgpack:"seen_inactive"`
	Unknown                  int      `json:"unknown" msgpack:"unknown"`
	InitialPresent           int      `json:"initial_present" msgpack:"initial_present"`
	Formations               int      `json:"formations" msgpack:"formations"`
	Losses                   int      `json:"losses" msgpack:"losses"`
	Regenerations            int      `json:"regenerations" msgpack:"regenerations"`
	Conversions              int      `json:"conversions" msgpack:"conversions"`
	CensoredEpisodes         int      `json:"censored_episodes" msgpack:"censored_episodes"`
	UncertainFormations      int      `json:"uncertain_formations" msgpack:"uncertain_formations"`
	UncertainLosses          int      `json:"uncertain_losses" msgpack:"uncertain_losses"`
	UncertainRegenerations   int      `json:"uncertain_regenerations" msgpack:"uncertain_regenerations"`
	UnknownEntries           int      `json:"unknown_entries" msgpack:"unknown_entries"`
	UnknownExits             int      `json:"unknown_exits" msgpack:"unknown_exits"`
	ActiveHolderRounds       int64    `json:"active_holder_rounds" msgpack:"active_holder_rounds"`
	KnownHolderRounds        int64    `json:"known_holder_rounds" msgpack:"known_holder_rounds"`
	NeverSeenHolderRounds    int64    `json:"never_seen_holder_rounds" msgpack:"never_seen_holder_rounds"`
	SeenInactiveHolderRounds int64    `json:"seen_inactive_holder_rounds" msgpack:"seen_inactive_holder_rounds"`
	UnknownHolderRounds      int64    `json:"unknown_holder_rounds" msgpack:"unknown_holder_rounds"`
}
