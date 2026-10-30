package moderation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

// DefaultSocket is where the chart mounts the control socket.
const DefaultSocket = "/run/tutti/moderation.sock"

// Client talks to the control server over its Unix socket.
type Client struct {
	http *http.Client
}

// NewClient returns a Client for the socket at path.
func NewClient(path string) *Client {
	return &Client{http: &http.Client{
		Timeout: timeout + 10*time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", path)
			},
			DisableKeepAlives: true,
		},
	}}
}

// Do posts req to path and decodes the answer into resp (nil to ignore it). A
// failed command is an *APIError.
func (c *Client) Do(ctx context.Context, path string, req, resp any) error {
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://moderation"+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	r.Header.Set("Content-Type", "application/json")
	res, err := c.http.Do(r)
	if err != nil {
		return fmt.Errorf("cannot reach the server's control socket: %w", err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return err
	}
	if res.StatusCode != http.StatusOK {
		var e ErrorResponse
		if json.Unmarshal(data, &e) != nil || e.Error == "" {
			return &APIError{Status: res.StatusCode, Code: "http_" + http.StatusText(res.StatusCode)}
		}
		return &APIError{Status: res.StatusCode, Code: e.Error, Message: e.Message}
	}
	if resp == nil {
		return nil
	}
	if err := json.Unmarshal(data, resp); err != nil {
		return fmt.Errorf("the server answered something unreadable: %w", err)
	}
	return nil
}

// IsCode reports whether err is an APIError with that code.
func IsCode(err error, code string) bool {
	var e *APIError
	return errors.As(err, &e) && e.Code == code
}
