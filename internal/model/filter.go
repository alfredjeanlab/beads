package model

// BeadFilter holds criteria for querying beads.
type BeadFilter struct {
	Status   []Status   `json:"status,omitempty"`
	Type     []BeadType `json:"type,omitempty"`
	Kind     []Kind     `json:"kind,omitempty"`
	Priority *int       `json:"priority,omitempty"`
	Assignee string     `json:"assignee,omitempty"`
	Labels   []string   `json:"labels,omitempty"`
	Search   string            `json:"search,omitempty"` // full-text search on title/description
	Fields   map[string]string `json:"fields,omitempty"` // custom field key=value filters (JSONB)
	Sort     string            `json:"sort,omitempty"`   // e.g. "-priority", "created_at"; prefix "-" = descending
	Limit    int        `json:"limit,omitempty"`
	Offset   int        `json:"offset,omitempty"`
}
