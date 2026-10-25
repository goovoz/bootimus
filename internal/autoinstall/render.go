package autoinstall

import (
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"bootimus/internal/models"
)

const VariablePrefix = "VAR."

var (
	placeholderPattern  = regexp.MustCompile(`\{\{(VAR\.[A-Za-z0-9_]+|[A-Z0-9_]+)\}\}`)
	variableNamePattern = regexp.MustCompile(`^[A-Za-z0-9_]+$`)
)

func Render(content string, vars map[string]string) (string, []string) {
	unresolved := []string{}
	seen := map[string]bool{}
	rendered := placeholderPattern.ReplaceAllStringFunc(content, func(token string) string {
		name := token[2 : len(token)-2]
		if value, ok := vars[name]; ok {
			return value
		}
		if !seen[name] {
			seen[name] = true
			unresolved = append(unresolved, name)
		}
		return token
	})
	return rendered, unresolved
}

func ValidateVariableNames(vars map[string]string) error {
	for name := range vars {
		if !variableNamePattern.MatchString(name) {
			return fmt.Errorf("invalid variable name %q: use letters, digits and underscores only", name)
		}
	}
	return nil
}

type Context struct {
	MAC        string
	ClientIP   string
	ServerAddr string
	ServerPort int
	Image      *models.Image
	Client     *models.Client
	Group      *models.ClientGroup
}

func (c Context) ServerURL() string {
	return fmt.Sprintf("http://%s:%d", c.ServerAddr, c.ServerPort)
}

func (c Context) Variables() map[string]string {
	vars := map[string]string{
		"MAC":          c.MAC,
		"CLIENT_NAME":  "",
		"HOSTNAME":     "",
		"IP":           c.ClientIP,
		"SERVER_ADDR":  c.ServerAddr,
		"SERVER_PORT":  strconv.Itoa(c.ServerPort),
		"SERVER_URL":   c.ServerURL(),
		"CLIENT_GROUP": "",
	}
	if c.Client != nil {
		vars["CLIENT_NAME"] = c.Client.Name
		vars["HOSTNAME"] = c.Client.Name
	}
	if c.Group != nil {
		vars["CLIENT_GROUP"] = c.Group.Name
		for name, value := range c.Group.Variables {
			vars[VariablePrefix+name] = value
		}
	}
	if c.Client != nil {
		for name, value := range c.Client.Variables {
			vars[VariablePrefix+name] = value
		}
	}
	if c.Image != nil {
		vars["IMAGE_NAME"] = c.Image.Name
		vars["IMAGE_FILENAME"] = c.Image.Filename
		vars["IMAGE_URL"] = c.ServerURL() + "/isos/" + encodePathSegments(c.Image.Filename)
	}
	return vars
}

func encodePathSegments(path string) string {
	segments := strings.Split(filepath.ToSlash(path), "/")
	for i, seg := range segments {
		segments[i] = url.PathEscape(seg)
	}
	return strings.Join(segments, "/")
}
