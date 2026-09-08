package jira

import (
	"context"
	"fmt"
	"strings"
)

// StatusCatalog is the status name <-> id mapping, fetched once at startup.
// Names are localized and can be renamed, ids cannot, so every comparison in
// the analytics layer is done on ids and this catalog exists only to translate
// configured names and to label the output.
type StatusCatalog struct {
	byID   map[string]string
	byName map[string][]string
}

// Statuses fetches the site-wide status catalog via GET /rest/api/3/status.
func (c *Client) Statuses(ctx context.Context) ([]Status, error) {
	var out []Status
	if err := c.do(ctx, "GET", "/rest/api/3/status", nil, nil, &out, ""); err != nil {
		return nil, fmt.Errorf("list statuses: %w", err)
	}
	return out, nil
}

// StatusCatalog builds the cached mapping.
func (c *Client) StatusCatalog(ctx context.Context) (*StatusCatalog, error) {
	statuses, err := c.Statuses(ctx)
	if err != nil {
		return nil, err
	}
	return NewStatusCatalog(statuses), nil
}

// NewStatusCatalog indexes a status list.
func NewStatusCatalog(statuses []Status) *StatusCatalog {
	cat := &StatusCatalog{
		byID:   make(map[string]string, len(statuses)),
		byName: make(map[string][]string, len(statuses)),
	}
	for _, s := range statuses {
		cat.byID[s.ID] = s.Name
		key := strings.ToLower(strings.TrimSpace(s.Name))
		cat.byName[key] = append(cat.byName[key], s.ID)
	}
	return cat
}

// Names returns the id -> name map for report labelling.
func (c *StatusCatalog) Names() map[string]string {
	out := make(map[string]string, len(c.byID))
	for k, v := range c.byID {
		out[k] = v
	}
	return out
}

// NameByID returns the current display name of a status id.
func (c *StatusCatalog) NameByID(id string) string { return c.byID[id] }

// Known reports whether a status id exists on this site.
func (c *StatusCatalog) Known(id string) bool { _, ok := c.byID[id]; return ok }

// IDsByName resolves a configured status name to ids. A name can map to several
// ids when projects use separate workflows, so all of them are returned.
func (c *StatusCatalog) IDsByName(name string) []string {
	return c.byName[strings.ToLower(strings.TrimSpace(name))]
}
