package replica

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	DefaultInterval = 5 * time.Minute
	MinInterval     = 30 * time.Second
)

type Config struct {
	PrimaryURL string
	Token      string
	Interval   time.Duration
	SyncISOs   bool
}

func (c *Config) Enabled() bool {
	return c != nil && strings.TrimSpace(c.PrimaryURL) != ""
}

func (c *Config) Validate() error {
	if !c.Enabled() {
		return nil
	}
	u, err := url.Parse(strings.TrimSpace(c.PrimaryURL))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("replica primary URL %q must be an http or https URL such as https://bootimus.example.com:8081", c.PrimaryURL)
	}
	if strings.TrimSpace(c.Token) == "" {
		return errors.New("replica mode needs an API token from the primary (--replica-token)")
	}
	if c.Interval == 0 {
		c.Interval = DefaultInterval
	}
	if c.Interval < MinInterval {
		return fmt.Errorf("replica sync interval %s is shorter than the minimum of %s", c.Interval, MinInterval)
	}
	return nil
}

func (c *Config) DisplayURL() string {
	u, err := url.Parse(strings.TrimSpace(c.PrimaryURL))
	if err != nil {
		return c.PrimaryURL
	}
	u.User = nil
	return strings.TrimRight(u.String(), "/")
}
