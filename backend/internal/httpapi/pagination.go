package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/denismurphy/agritelem/backend/internal/store"
)

const defaultPageSize = 100

type pageToken struct {
	Modified time.Time `json:"m"`
	Key1     string    `json:"a"`
	Key2     string    `json:"b,omitempty"`
}

func encodePageToken(c store.Cursor) string {
	b, _ := json.Marshal(pageToken{Modified: c.Modified.UTC(), Key1: c.Key1, Key2: c.Key2}) // cannot fail for this type
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodePageToken(s *string) (store.Cursor, error) {
	if s == nil || *s == "" {
		return store.Cursor{}, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(*s)
	if err != nil {
		return store.Cursor{}, fmt.Errorf("%w: page-token is malformed", errBadRequest)
	}
	var t pageToken
	if err := json.Unmarshal(raw, &t); err != nil {
		return store.Cursor{}, fmt.Errorf("%w: page-token is malformed", errBadRequest)
	}
	return store.Cursor{Modified: t.Modified, Key1: t.Key1, Key2: t.Key2}, nil
}

func pageSize(p *int) int {
	if p == nil {
		return defaultPageSize
	}
	return *p
}

// nextURL returns the absolute URL of the next page: the request URL with
// page-token replaced.
func nextURL(base *url.URL, path string, query url.Values, token string, size int) string {
	q := url.Values{}
	for k, v := range query {
		q[k] = append([]string(nil), v...)
	}
	q.Set("page-token", token)
	q.Set("page-size", strconv.Itoa(size))
	u := *base
	u.Path = path
	u.RawQuery = q.Encode()
	return u.String()
}
