package team

import "encoding/json"

// AppearanceRoute is PUT: edit a live team's name, short label and colour (TR-1, team-interface spec §4.12).
const AppearanceRoute = "/api/team/appearance"

// MaxTeamColor is the highest colour index (the App's eight swatches are 0–7).
const MaxTeamColor = 7

// ErrNotLive is the 409 of the appearance edit: the team ended meanwhile.
const ErrNotLive = "not_live"

// AppearancePutRequest is the body: ALL of the fields every time (the App sends the roster's current values with its
// changes), so a missing one is a 400, never "leave it as it is". TeamColor is the raw JSON so that an absent key and
// null (automatic) can be told apart: a number 0–7, or null.
type AppearancePutRequest struct {
	TeamID    string          `json:"team_id"`
	TeamName  *string         `json:"team_name"`
	TeamLabel *string         `json:"team_label"` // "" = derived from the name by the creation rule, not cleared
	TeamColor json.RawMessage `json:"team_color"`
	Client    Client          `json:"client"` // recorded in the log, not checked
}

// AppearanceView is the 200 answer: what was stored. TeamColor is null for automatic.
type AppearanceView struct {
	TeamID    string `json:"team_id"`
	TeamName  string `json:"team_name"`
	TeamLabel string `json:"team_label"`
	TeamColor *int   `json:"team_color"`
}
